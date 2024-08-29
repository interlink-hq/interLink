package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	logruslogger "github.com/virtual-kubelet/virtual-kubelet/log/logrus"
	"github.com/virtual-kubelet/virtual-kubelet/trace"
	"github.com/virtual-kubelet/virtual-kubelet/trace/opentelemetry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	types "github.com/intertwin-eu/interlink/pkg/interlink"
	"github.com/intertwin-eu/interlink/pkg/interlink/api"
	"github.com/intertwin-eu/interlink/pkg/virtualkubelet"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
)

func initProvider(ctx context.Context) (func(context.Context) error, error) {
	res, err := resource.New(ctx,
		resource.WithAttributes(
			// the service name used to display traces in backends
			semconv.ServiceName("InterLink-API"),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	otlpEndpoint := os.Getenv("TELEMETRY_ENDPOINT")

	if otlpEndpoint == "" {
		otlpEndpoint = "localhost:4317"
	}

	fmt.Println("TELEMETRY_ENDPOINT: ", otlpEndpoint)

	conn := &grpc.ClientConn{}
	creds := credentials.NewTLS(&tls.Config{InsecureSkipVerify: true})
	conn, err = grpc.DialContext(ctx, otlpEndpoint, grpc.WithTransportCredentials(creds), grpc.WithBlock())

	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC connection to collector: %w", err)
	}

	// Set up a trace exporter
	traceExporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithGRPCConn(conn))
	if err != nil {
		return nil, fmt.Errorf("failed to create trace exporter: %w", err)
	}

	// Register the trace exporter with a TracerProvider, using a batch
	// span processor to aggregate spans before export.
	bsp := sdktrace.NewBatchSpanProcessor(traceExporter)
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithResource(res),
		sdktrace.WithSpanProcessor(bsp),
	)
	otel.SetTracerProvider(tracerProvider)

	// set global propagator to tracecontext (the default is no-op).
	otel.SetTextMapPropagator(propagation.TraceContext{})

	return tracerProvider.Shutdown, nil
}

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
		shutdown, err := initProvider(ctx)
		if err != nil {
			log.G(ctx).Fatal(err)
		}
		defer func() {
			if err = shutdown(ctx); err != nil {
				log.G(ctx).Fatal("failed to shutdown TracerProvider: %w", err)
			}
		}()

		log.G(ctx).Info("Tracer setup succeeded")

		// TODO: disable this through options
		trace.T = opentelemetry.Adapter{}
	}

	log.G(ctx).Info(interLinkConfig)

	log.G(ctx).Info("interLink version: ", virtualkubelet.KubeletVersion)

	sidecarEndpoint := ""
	if strings.HasPrefix(interLinkConfig.Sidecarurl, "unix://") {
		sidecarEndpoint = interLinkConfig.Sidecarurl
	} else if strings.HasPrefix(interLinkConfig.Sidecarurl, "http://") {
		sidecarEndpoint = interLinkConfig.Sidecarurl + ":" + interLinkConfig.Sidecarport
	} else {
		log.G(ctx).Fatal("Sidecar URL should either start per unix:// or http://")
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
	if strings.HasPrefix(interLinkConfig.InterlinkAddress, "unix://") {
		interLinkEndpoint = interLinkConfig.InterlinkAddress
	} else if strings.HasPrefix(interLinkConfig.InterlinkAddress, "http://") {
		interLinkEndpoint = strings.Replace(interLinkConfig.InterlinkAddress, "http://", "", -1) + ":" + interLinkConfig.Interlinkport
	} else {
		log.G(ctx).Fatal("Sidecar URL should either start per unix:// or http://")
	}

	err = http.ListenAndServe(interLinkEndpoint, mutex)

	if err != nil {
		log.G(ctx).Fatal(err)
	}
}
