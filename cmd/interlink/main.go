package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	logruslogger "github.com/virtual-kubelet/virtual-kubelet/log/logrus"
	"github.com/virtual-kubelet/virtual-kubelet/trace"
	"github.com/virtual-kubelet/virtual-kubelet/trace/opentelemetry"

	"github.com/interlink-hq/interlink/pkg/interlink"
	"github.com/interlink-hq/interlink/pkg/interlink/api"
	vkconfig "github.com/interlink-hq/interlink/pkg/virtualkubelet"
)

// UnixSocketRoundTripper is a custom RoundTripper for Unix socket connections
type UnixSocketRoundTripper struct {
	Transport http.RoundTripper
}

func (rt *UnixSocketRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.HasPrefix(req.URL.Scheme, "http+unix") {
		// Adjust the URL for Unix socket connections
		req.URL.Scheme = "http"
		req.URL.Host = "unix"
	}
	return rt.Transport.RoundTrip(req)
}

// createTLSConfig creates TLS configuration for the interLink server
func createTLSConfig(ctx context.Context, tlsConfig interlink.TLSConfig) (*tls.Config, error) {
	if !tlsConfig.Enabled {
		return nil, nil
	}

	config := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}

	// Load server certificate and key
	if tlsConfig.CertFile != "" && tlsConfig.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(tlsConfig.CertFile, tlsConfig.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load server certificate pair (%s, %s): %w", tlsConfig.CertFile, tlsConfig.KeyFile, err)
		}
		config.Certificates = []tls.Certificate{cert}
		log.G(ctx).Info("Loaded server certificate for TLS from: ", tlsConfig.CertFile, " and ", tlsConfig.KeyFile)
	} else {
		return nil, fmt.Errorf("TLS enabled but CertFile or KeyFile not provided")
	}

	// Load CA certificate for client verification (mTLS)
	if tlsConfig.CACertFile != "" {
		caCert, err := os.ReadFile(tlsConfig.CACertFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA certificate file %s: %w", tlsConfig.CACertFile, err)
		}

		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA certificate from %s", tlsConfig.CACertFile)
		}
		config.ClientCAs = caCertPool
		config.ClientAuth = tls.RequireAndVerifyClientCert
		log.G(ctx).Info("Loaded CA certificate for mTLS client verification from: ", tlsConfig.CACertFile)
		log.G(ctx).Info("mTLS enabled - clients must provide valid certificates")
	} else {
		log.G(ctx).Info("TLS enabled without client certificate verification")
	}

	return config, nil
}

func main() {
	printVersion := flag.Bool("version", false, "show version")
	flag.Parse()

	if *printVersion {
		fmt.Println(vkconfig.KubeletVersion)
		return
	}
	var cancel context.CancelFunc
	api.PodStatuses.Statuses = make(map[string]interlink.PodStatus)

	interLinkConfig, err := interlink.NewInterLinkConfig()
	if err != nil {
		panic(err)
	}
	logger := logrus.StandardLogger()

	logger.SetLevel(logrus.InfoLevel)
	if interLinkConfig.VerboseLogging {
		logger.SetLevel(logrus.DebugLevel)
	} else if interLinkConfig.ErrorsOnlyLogging {
		logger.SetLevel(logrus.ErrorLevel)
	}

	log.L = logruslogger.FromLogrus(logrus.NewEntry(logger))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if os.Getenv("ENABLE_TRACING") == "1" {
		shutdown, err := interlink.InitTracer(ctx, "InterLink-Plugin-")
		if err != nil {
			log.G(ctx).Fatal(err)
		}
		defer func() {
			if err = shutdown(ctx); err != nil {
				log.G(ctx).Fatal("failed to shutdown TracerProvider: %w", err)
			}
		}()

		log.G(ctx).Info("Tracer setup succeeded")

		trace.T = opentelemetry.Adapter{}
	}

	log.G(ctx).Info(interLinkConfig)

	log.G(ctx).Info("interLink version: ", vkconfig.KubeletVersion)

	sidecarEndpoint := ""
	var socketPath string

	switch {
	case strings.HasPrefix(interLinkConfig.Sidecarurl, "unix://"):
		socketPath = strings.ReplaceAll(interLinkConfig.Sidecarurl, "unix://", "")
		sidecarEndpoint = "http+unix://"
	case strings.HasPrefix(interLinkConfig.Sidecarurl, "http://"):
		sidecarEndpoint = interLinkConfig.Sidecarurl + ":" + interLinkConfig.Sidecarport
	default:
		log.G(ctx).Fatal("Sidecar URL should either start per unix:// or http://: getting ", interLinkConfig.Sidecarurl)
	}

	dialer := &net.Dialer{
		Timeout:   90 * time.Second,
		KeepAlive: 90 * time.Second,
	}
	transport := &http.Transport{
		MaxConnsPerHost:       10000,
		MaxIdleConnsPerHost:   1000,
		IdleConnTimeout:       120 * time.Second,
		ResponseHeaderTimeout: 120 * time.Second,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if strings.HasPrefix(addr, "unix:") {
				return dialer.DialContext(ctx, "unix", socketPath)
			}
			return dialer.DialContext(ctx, network, addr)
		},
	}

	clientHTTP := &http.Client{
		Transport: &UnixSocketRoundTripper{
			Transport: transport,
		},
	}

	interLinkAPIs := api.InterLinkHandler{
		Config:          interLinkConfig,
		Ctx:             ctx,
		SidecarEndpoint: sidecarEndpoint,
		ClientHTTP:      clientHTTP,
	}

	mutex := http.NewServeMux()
	mutex.HandleFunc("/status", interLinkAPIs.StatusHandler)
	mutex.HandleFunc("/create", interLinkAPIs.CreateHandler)
	mutex.HandleFunc("/delete", interLinkAPIs.DeleteHandler)
	mutex.HandleFunc("/pinglink", interLinkAPIs.Ping)
	mutex.HandleFunc("/getLogs", interLinkAPIs.GetLogsHandler)
	mutex.HandleFunc("/updateCache", interLinkAPIs.UpdateCacheHandler)

	interLinkEndpoint := ""
	switch {
	case strings.HasPrefix(interLinkConfig.InterlinkAddress, "unix://"):
		interLinkEndpoint = interLinkConfig.InterlinkAddress

		// Create a Unix domain socket and listen for incoming connections.
		socket, err := net.Listen("unix", strings.ReplaceAll(interLinkEndpoint, "unix://", ""))
		if err != nil {
			panic(err)
		}

		// Cleanup the sockfile.
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt, syscall.SIGTERM)
		go func() {
			<-c
			os.Remove(strings.ReplaceAll(interLinkEndpoint, "unix://", ""))
			os.Exit(1)
		}()
		server := http.Server{
			Handler: mutex,
		}

		log.G(ctx).Info(socket)

		if err := server.Serve(socket); err != nil {
			log.G(ctx).Fatal(err)
		}
	case strings.HasPrefix(interLinkConfig.InterlinkAddress, "http://"):
		interLinkEndpoint = strings.ReplaceAll(interLinkConfig.InterlinkAddress, "http://", "") + ":" + interLinkConfig.Interlinkport

		server := http.Server{
			Addr:              interLinkEndpoint,
			Handler:           mutex,
			ReadTimeout:       30 * time.Second,
			ReadHeaderTimeout: 10 * time.Second,
		}

		err = server.ListenAndServe()
		if err != nil {
			log.G(ctx).Fatal(err)
		}
	case strings.HasPrefix(interLinkConfig.InterlinkAddress, "https://"):
		interLinkEndpoint = strings.ReplaceAll(interLinkConfig.InterlinkAddress, "https://", "") + ":" + interLinkConfig.Interlinkport

		// Create TLS configuration
		tlsConfig, err := createTLSConfig(ctx, interLinkConfig.TLS)
		if err != nil {
			log.G(ctx).Fatal("Failed to create TLS configuration: ", err)
		}

		server := http.Server{
			Addr:              interLinkEndpoint,
			Handler:           mutex,
			ReadTimeout:       30 * time.Second,
			ReadHeaderTimeout: 10 * time.Second,
			TLSConfig:         tlsConfig,
		}

		log.G(ctx).Info("Starting HTTPS server on: ", interLinkEndpoint)
		if tlsConfig != nil && tlsConfig.ClientAuth == tls.RequireAndVerifyClientCert {
			log.G(ctx).Info("mTLS enabled - requiring client certificates")
		}

		// Use ListenAndServeTLS with cert files from config
		err = server.ListenAndServeTLS(interLinkConfig.TLS.CertFile, interLinkConfig.TLS.KeyFile)
		if err != nil {
			log.G(ctx).Fatal(err)
		}
	default:
		log.G(ctx).Fatal("Interlink URL should start with unix://, http://, or https://. Getting: ", interLinkConfig.InterlinkAddress)
	}
}
