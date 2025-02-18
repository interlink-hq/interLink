package interlink

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/containerd/containerd/log"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"gopkg.in/yaml.v2"
)

type VolumesOptions struct {
	ScratchArea                 string   `yaml:"scratch_area"`
	ApptainerCacheDir           string   `yaml:"apptainer_cachedir"`
	ImageDir                    string   `yaml:"image_dir"`
	AdditionalDirectoriesInPath []string `yaml:"additional_directories_in_path"`
	FuseSleepSeconds            int      `yaml:"fuse_sleep_seconds"`
}

type SingularityHubConfig struct {
	Server               string `yaml:"server"`
	MasterToken          string `yaml:"master_token"`
	CacheValiditySeconds int    `yaml:"cache_validity_seconds"`
}

type ApptainerOptions struct {
	Executable    string `yaml:"executable"`
	Fakeroot      bool   `yaml:"fakeroot"`
	ContainAll    bool   `yaml:"containall"`
	FuseMode      string `yaml:"fuse_mode"`
	NoInit        bool   `yaml:"no_init"`
	NoHome        bool   `yaml:"no_home"`
	NoPrivs       bool   `yaml:"no_privs"`
	NvidiaSupport bool   `yaml:"nvidia_support"`
	Cleanenv      bool   `yaml:"cleanenv"`
	Unsquash      bool   `yaml:"unsquash"`
}

type ScriptBuildConfig struct {
	SingularityHub   SingularityHubConfig `yaml:"singularity_hub"`
	ApptainerOptions ApptainerOptions     `yaml:"apptainer_options"`
	VolumesOptions   VolumesOptions       `yaml:"volumes_options"`
}

// Config holds the whole configuration
type Config struct {
	InterlinkAddress     string             `yaml:"InterlinkAddress"`
	Interlinkport        string             `yaml:"InterlinkPort"`
	Sidecarurl           string             `yaml:"SidecarURL"`
	Sidecarport          string             `yaml:"SidecarPort"`
	JobScriptBuildConfig *ScriptBuildConfig `yaml:"JobScriptBuildConfig,omitempty"`
	JobScriptTemplate    string             `yaml:"JobScriptTemplate,omitempty"`
	VerboseLogging       bool               `yaml:"VerboseLogging"`
	ErrorsOnlyLogging    bool               `yaml:"ErrorsOnlyLogging"`
	DataRootFolder       string             `yaml:"DataRootFolder"`
}

func SetupTelemetry(ctx context.Context, serviceName string) (*sdktrace.TracerProvider, error) {
	log.G(ctx).Info("Tracing is enabled, setting up the TracerProvider")

	// Get the TELEMETRY_UNIQUE_ID from the environment, if it is not set, use the hostname
	uniqueID := os.Getenv("TELEMETRY_UNIQUE_ID")
	if uniqueID == "" {
		log.G(ctx).Info("No TELEMETRY_UNIQUE_ID set, generating a new one")
		newUUID := uuid.New()
		uniqueID = newUUID.String()
		log.G(ctx).Info("Generated unique ID: ", uniqueID, " use "+serviceName+"-"+uniqueID+" as service name from Grafana")
	}

	fullServiceName := serviceName + uniqueID

	res, err := resource.New(ctx,
		resource.WithAttributes(
			// the service name used to display traces in backends
			semconv.ServiceName(fullServiceName),
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

	log.G(ctx).Info("TELEMETRY_ENDPOINT: ", otlpEndpoint)

	caCrtFilePath := os.Getenv("TELEMETRY_CA_CRT_FILEPATH")

	conn := &grpc.ClientConn{}
	if caCrtFilePath != "" {

		// if the CA certificate is provided, set up mutual TLS

		log.G(ctx).Info("CA certificate provided, setting up mutual TLS")

		caCert, err := os.ReadFile(caCrtFilePath)
		if err != nil {
			return nil, fmt.Errorf("failed to load CA certificate: %w", err)
		}

		clientKeyFilePath := os.Getenv("TELEMETRY_CLIENT_KEY_FILEPATH")
		if clientKeyFilePath == "" {
			return nil, fmt.Errorf("client key file path not provided. Since a CA certificate is provided, a client key is required for mutual TLS")
		}

		clientCrtFilePath := os.Getenv("TELEMETRY_CLIENT_CRT_FILEPATH")
		if clientCrtFilePath == "" {
			return nil, fmt.Errorf("client certificate file path not provided. Since a CA certificate is provided, a client certificate is required for mutual TLS")
		}

		insecureSkipVerify := false
		if os.Getenv("TELEMETRY_INSECURE_SKIP_VERIFY") == "true" {
			insecureSkipVerify = true
		}

		certPool := x509.NewCertPool()
		if !certPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to append CA certificate")
		}

		cert, err := tls.LoadX509KeyPair(clientCrtFilePath, clientKeyFilePath)
		if err != nil {
			return nil, fmt.Errorf("failed to load client certificate: %w", err)
		}

		tlsConfig := &tls.Config{
			Certificates:       []tls.Certificate{cert},
			RootCAs:            certPool,
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: insecureSkipVerify, // #nosec
		}

		creds := credentials.NewTLS(tlsConfig)
		conn, err = grpc.NewClient(otlpEndpoint, grpc.WithTransportCredentials(creds))
		if err != nil {
			return nil, fmt.Errorf("failed to create gRPC client: %w", err)
		}
	} else {
		conn, err = grpc.NewClient(otlpEndpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	conn.WaitForStateChange(ctx, connectivity.Ready)

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

	return tracerProvider, nil
}

func InitTracer(ctx context.Context, serviceName string) (func(context.Context) error, error) {
	// Get the TELEMETRY_UNIQUE_ID from the environment, if it is not set, use the hostname
	tracerProvider, err := SetupTelemetry(ctx, serviceName)
	if err != nil {
		return nil, err
	}

	return tracerProvider.Shutdown, nil
}

// NewInterLinkConfig returns a variable of type InterLinkConfig, used in many other functions and the first encountered error.
func NewInterLinkConfig() (Config, error) {
	var path string
	verbose := flag.Bool("verbose", false, "Enable or disable Debug level logging")
	errorsOnly := flag.Bool("errorsonly", false, "Prints only errors if enabled")
	InterLinkConfigPath := flag.String("interlinkconfigpath", "", "Path to InterLink config")
	flag.Parse()

	interLinkNewConfig := Config{}

	if *verbose {
		interLinkNewConfig.VerboseLogging = true
		interLinkNewConfig.ErrorsOnlyLogging = false
	} else if *errorsOnly {
		interLinkNewConfig.VerboseLogging = false
		interLinkNewConfig.ErrorsOnlyLogging = true
	}

	if *InterLinkConfigPath != "" {
		path = *InterLinkConfigPath
	} else {
		if os.Getenv("INTERLINKCONFIGPATH") != "" {
			path = os.Getenv("INTERLINKCONFIGPATH")
		} else {
			path = "/etc/interlink/InterLinkConfig.yaml"
		}
	}

	if _, err := os.Stat(path); err != nil {
		log.G(context.Background()).Error("File " + path + " doesn't exist. You can set a custom path by exporting INTERLINKCONFIGPATH. Exiting...")
		return Config{}, err
	}

	log.G(context.Background()).Info("Loading InterLink config from " + path)
	yfile, err := os.ReadFile(path)
	if err != nil {
		log.G(context.Background()).Error("Error opening config file, exiting...")
		return Config{}, err
	}

	err = yaml.Unmarshal(yfile, &interLinkNewConfig)
	if err != nil {
		return Config{}, err
	}

	if os.Getenv("INTERLINKURL") != "" {
		interLinkNewConfig.InterlinkAddress = os.Getenv("INTERLINKURL")
	}

	if os.Getenv("SIDECARURL") != "" {
		interLinkNewConfig.Sidecarurl = os.Getenv("SIDECARURL")
	}

	if os.Getenv("INTERLINKPORT") != "" {
		interLinkNewConfig.Interlinkport = os.Getenv("INTERLINKPORT")
	}

	if os.Getenv("SIDECARPORT") != "" {
		interLinkNewConfig.Sidecarport = os.Getenv("SIDECARPORT")
	}

	return interLinkNewConfig, nil
}
