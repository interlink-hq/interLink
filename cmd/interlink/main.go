package main

import (
	"context"
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

	"github.com/intertwin-eu/interlink/pkg/interlink"
	types "github.com/intertwin-eu/interlink/pkg/interlink"
	"github.com/intertwin-eu/interlink/pkg/interlink/api"
	"github.com/intertwin-eu/interlink/pkg/virtualkubelet"
)

func main() {
	printVersion := flag.Bool("version", false, "show version")
	flag.Parse()

	if *printVersion {
		fmt.Println(virtualkubelet.KubeletVersion)
		return
	}
	var cancel context.CancelFunc
	api.PodStatuses.Statuses = make(map[string]types.PodStatus)

	interLinkConfig, err := types.NewInterLinkConfig()
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
		shutdown, err := interlink.InitTracer(ctx)
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

	log.G(ctx).Info("interLink version: ", virtualkubelet.KubeletVersion)

	http.DefaultTransport.(*http.Transport).MaxConnsPerHost = 10000
	http.DefaultTransport.(*http.Transport).MaxIdleConnsPerHost = 1000
	http.DefaultTransport.(*http.Transport).IdleConnTimeout = 120 * time.Second
	http.DefaultTransport.(*http.Transport).ResponseHeaderTimeout = 120 * time.Second

	sidecarEndpoint := ""
	switch {
	case strings.HasPrefix(interLinkConfig.Sidecarurl, "unix://"):
		sidecarEndpoint = strings.ReplaceAll(interLinkConfig.Sidecarurl, "unix://", "")
		// Dial the Unix socket
		var conn net.Conn
		for {
			conn, err = net.Dial("unix", sidecarEndpoint)
			if err != nil {
				log.G(ctx).Error(err)
				time.Sleep(30 * time.Second)
			} else {
				break
			}
		}

		http.DefaultTransport.(*http.Transport).DialContext = func(_ context.Context, _, _ string) (net.Conn, error) {
			return conn, nil
		}
		sidecarEndpoint = "http://unix"
	case strings.HasPrefix(interLinkConfig.Sidecarurl, "http://"):
		sidecarEndpoint = interLinkConfig.Sidecarurl + ":" + interLinkConfig.Sidecarport
	default:
		log.G(ctx).Fatal("Sidecar URL should either start per unix:// or http://: getting ", interLinkConfig.Sidecarurl)
	}

	interLinkAPIs := api.InterLinkHandler{
		Config:          interLinkConfig,
		Ctx:             ctx,
		SidecarEndpoint: sidecarEndpoint,
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
	default:
		log.G(ctx).Fatal("Interlink URL should either start per unix:// or http://. Getting: ", interLinkConfig.InterlinkAddress)
	}
}
