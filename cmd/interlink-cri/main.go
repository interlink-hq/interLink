package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/sirupsen/logrus"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	logruslogger "github.com/virtual-kubelet/virtual-kubelet/log/logrus"
	"github.com/virtual-kubelet/virtual-kubelet/trace"
	"github.com/virtual-kubelet/virtual-kubelet/trace/opentelemetry"
	"k8s.io/cri-client/pkg/util"

	"github.com/interlink-hq/interlink/pkg/interlink"
	vkconfig "github.com/interlink-hq/interlink/pkg/virtualkubelet"
)

const (
	CRIVersion = "0.6.0-dev"
	trueValue  = "true"
)

func main() {
	var (
		printVersion = flag.Bool("version", false, "show version")
		configPath   = flag.String("config", "", "path to configuration file")
		socketPath   = flag.String("socket", "unix:///var/run/interlink/cri.sock", "CRI socket path")
	)
	flag.Parse()

	if *printVersion {
		fmt.Printf("InterLink CRI %s\n", CRIVersion)
		return
	}

	// Load configuration
	var criConfig vkconfig.Config
	if *configPath != "" {
		// Load from file (implement config loading similar to interLink)
		log.L.Info("Loading CRI configuration from: ", *configPath)
		// For now, use environment variables and defaults
	}

	// Set up from environment variables if config not provided
	if criConfig.InterlinkURL == "" {
		criConfig.InterlinkURL = getEnvOrDefault("INTERLINK_URL", "https://localhost:8080")
	}
	if criConfig.InterlinkPort == "" {
		criConfig.InterlinkPort = getEnvOrDefault("INTERLINK_PORT", "8080")
	}
	criConfig.VKTokenFile = os.Getenv("VK_TOKEN_FILE")
	criConfig.VerboseLogging = getEnvOrDefault("INTERLINK_CRI_VERBOSE", "false") == trueValue
	criConfig.ErrorsOnlyLogging = getEnvOrDefault("INTERLINK_CRI_ERRORS_ONLY", "false") == trueValue

	// TLS configuration from environment
	criConfig.TLS.Enabled = getEnvOrDefault("INTERLINK_TLS_ENABLED", "false") == trueValue
	criConfig.TLS.CertFile = os.Getenv("INTERLINK_TLS_CERT_FILE")
	criConfig.TLS.KeyFile = os.Getenv("INTERLINK_TLS_KEY_FILE")
	criConfig.TLS.CACertFile = os.Getenv("INTERLINK_TLS_CA_FILE")

	// Set up logging
	logger := logrus.StandardLogger()
	logger.SetLevel(logrus.InfoLevel)
	if criConfig.VerboseLogging {
		logger.SetLevel(logrus.DebugLevel)
	} else if criConfig.ErrorsOnlyLogging {
		logger.SetLevel(logrus.ErrorLevel)
	}

	log.L = logruslogger.FromLogrus(logrus.NewEntry(logger))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Set up tracing if enabled
	if os.Getenv("ENABLE_TRACING") == "1" {
		shutdown, err := interlink.InitTracer(ctx, "InterLink-CRI-")
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

	log.G(ctx).Info("Starting InterLink CRI version: ", CRIVersion)
	log.G(ctx).Info("InterLink API server: ", criConfig.InterlinkURL, ":", criConfig.InterlinkPort)
	log.G(ctx).Info("CRI socket: ", *socketPath)

	// Create and start the CRI runtime
	interlinkRuntime := NewFakeRemoteRuntime(ctx, criConfig)

	// Handle shutdown gracefully
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		log.G(ctx).Info("Received shutdown signal, stopping CRI runtime...")
		interlinkRuntime.Stop()

		// Clean up socket file
		if addr, _, err := util.GetAddressAndDialer(*socketPath); err == nil {
			if _, err := os.Stat(addr); err == nil {
				os.Remove(addr)
				log.G(ctx).Info("Cleaned up socket file: ", addr)
			}
		}
		os.Exit(0)
	}()

	// Start the CRI server
	err := interlinkRuntime.Start(*socketPath)
	if err != nil {
		interlinkRuntime.Stop()
		log.G(ctx).Fatal("Failed to start CRI server: ", err)
	}

	// Keep the main goroutine alive
	select {}
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
