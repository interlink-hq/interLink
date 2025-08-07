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

// VolumesOptions configures volume management for container runtimes like Apptainer.
// It specifies directories and settings for container filesystem operations.
type VolumesOptions struct {
	// ScratchArea specifies the temporary workspace directory for container operations
	ScratchArea string `json:"scratch_area" yaml:"scratch_area"`
	// ApptainerCacheDir is the directory where Apptainer stores cached container images
	ApptainerCacheDir string `json:"apptainer_cachedir" yaml:"apptainer_cachedir"`
	// ImageDir specifies where container images are stored
	ImageDir string `json:"image_dir" yaml:"image_dir"`
	// AdditionalDirectoriesInPath adds extra directories to the container's PATH
	AdditionalDirectoriesInPath []string `json:"additional_directories_in_path" yaml:"additional_directories_in_path"`
	// FuseSleepSeconds sets the delay before mounting FUSE filesystems
	FuseSleepSeconds int `json:"fuse_sleep_seconds" yaml:"fuse_sleep_seconds"`
}

// SingularityHubConfig configures access to SingularityHub or compatible registries.
// This is used for pulling container images from Singularity-compatible repositories.
type SingularityHubConfig struct {
	// Server is the URL of the SingularityHub server
	Server string `json:"server" yaml:"server"`
	// MasterToken is the authentication token for accessing the hub
	MasterToken string `json:"master_token" yaml:"master_token"`
	// CacheValiditySeconds specifies how long to cache image metadata
	CacheValiditySeconds int `json:"cache_validity_seconds" yaml:"cache_validity_seconds"`
}

// ApptainerOptions configures Apptainer/Singularity container runtime behavior.
// These options control security, isolation, and runtime features.
type ApptainerOptions struct {
	// Executable specifies the path to the Apptainer binary
	Executable string `json:"executable" yaml:"executable"`
	// Fakeroot enables fake root privileges inside containers
	Fakeroot bool `json:"fakeroot" yaml:"fakeroot"`
	// ContainAll enables maximum container isolation
	ContainAll bool `json:"containall" yaml:"containall"`
	// FuseMode specifies the FUSE mounting mode (e.g., "kernel", "userspace")
	FuseMode string `json:"fuseMode" yaml:"fuse_mode"`
	// NoInit disables the container init process
	NoInit bool `json:"noInit" yaml:"no_init"`
	// NoHome prevents mounting the user's home directory
	NoHome bool `json:"noHome" yaml:"no_home"`
	// NoPrivs drops all privileges in the container
	NoPrivs bool `json:"noPrivs" yaml:"no_privs"`
	// NvidiaSupport enables NVIDIA GPU support in containers
	NvidiaSupport bool `json:"nvidiaSupport" yaml:"nvidia_support"`
	// Cleanenv starts containers with a clean environment
	Cleanenv bool `json:"cleanenv" yaml:"cleanenv"`
	// Unsquash extracts SquashFS images before execution
	Unsquash bool `json:"unsquash" yaml:"unsquash"`
}

// ScriptBuildConfig aggregates all configuration needed for building job scripts.
// This is used when interLink needs to generate custom job scripts for HPC systems.
type ScriptBuildConfig struct {
	// SingularityHub contains SingularityHub access configuration
	SingularityHub SingularityHubConfig `json:"SingularityHubProxy" yaml:"singularity_hub"`
	// ApptainerOptions contains Apptainer runtime configuration
	ApptainerOptions ApptainerOptions `json:"ApptainerOptions" yaml:"apptainer_options"`
	// VolumesOptions contains volume management configuration
	VolumesOptions VolumesOptions `json:"Volumes" yaml:"volumes_options"`
}

// Config holds the complete interLink API server configuration.
// It defines how the interLink server connects to sidecar plugins, handles TLS,
// and manages job script generation.
type Config struct {
	// InterlinkAddress is the address where the interLink API server listens
	// Supports unix://, http://, and https:// schemes
	InterlinkAddress string `yaml:"InterlinkAddress"`
	// Interlinkport is the port for the interLink API server (for http/https)
	Interlinkport string `yaml:"InterlinkPort"`
	// Sidecarurl is the URL of the sidecar plugin
	// Supports unix:// and http:// schemes
	Sidecarurl string `yaml:"SidecarURL"`
	// Sidecarport is the port of the sidecar plugin (for http)
	Sidecarport string `yaml:"SidecarPort"`
	// JobScriptBuildConfig contains configuration for building job scripts (optional)
	JobScriptBuildConfig *ScriptBuildConfig `yaml:"JobScriptBuildConfig,omitempty"`
	// JobScriptTemplate is the path to a local job script template file (optional)
	JobScriptTemplate string `yaml:"JobScriptTemplate,omitempty"`
	// VerboseLogging enables debug-level logging
	VerboseLogging bool `yaml:"VerboseLogging"`
	// ErrorsOnlyLogging restricts logging to errors only
	ErrorsOnlyLogging bool `yaml:"ErrorsOnlyLogging"`
	// DataRootFolder is the base directory for interLink data storage
	DataRootFolder string `yaml:"DataRootFolder"`
	// TLS contains TLS/mTLS configuration for secure communication
	TLS TLSConfig `yaml:"TLS,omitempty"`
}

// TLSConfig holds TLS/mTLS configuration for secure communication.
// It supports both server-side TLS and mutual TLS (mTLS) authentication.
type TLSConfig struct {
	// Enabled indicates whether TLS is enabled
	Enabled bool `yaml:"Enabled"`
	// CertFile is the path to the server certificate file
	CertFile string `yaml:"CertFile,omitempty"`
	// KeyFile is the path to the server private key file
	KeyFile string `yaml:"KeyFile,omitempty"`
	// CACertFile is the path to the CA certificate for client verification (mTLS)
	CACertFile string `yaml:"CACertFile,omitempty"`
}

// SetupTelemetry initializes OpenTelemetry tracing with OTLP export.
// It configures distributed tracing for the interLink service, supporting both
// insecure and mTLS connections to the telemetry collector.
//
// Environment variables:
//   - TELEMETRY_UNIQUE_ID: Unique identifier for this service instance
//   - TELEMETRY_ENDPOINT: OTLP collector endpoint (default: localhost:4317)
//   - TELEMETRY_CA_CRT_FILEPATH: CA certificate for mTLS
//   - TELEMETRY_CLIENT_CRT_FILEPATH: Client certificate for mTLS
//   - TELEMETRY_CLIENT_KEY_FILEPATH: Client private key for mTLS
//   - TELEMETRY_INSECURE_SKIP_VERIFY: Skip TLS verification (for testing)
//
// Returns a TracerProvider configured for the service, or an error if setup fails.
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

// InitTracer initializes distributed tracing for the interLink service.
// This is a convenience wrapper around SetupTelemetry that returns a shutdown function.
// The shutdown function should be called when the service terminates to properly
// flush any remaining traces.
//
// Returns a shutdown function and any initialization error.
func InitTracer(ctx context.Context, serviceName string) (func(context.Context) error, error) {
	// Get the TELEMETRY_UNIQUE_ID from the environment, if it is not set, use the hostname
	tracerProvider, err := SetupTelemetry(ctx, serviceName)
	if err != nil {
		return nil, err
	}

	return tracerProvider.Shutdown, nil
}

// NewInterLinkConfig loads and returns the interLink configuration.
// It reads configuration from a YAML file, with the path determined by:
//  1. Command line flag --interlinkconfigpath
//  2. Environment variable INTERLINKCONFIGPATH
//  3. Default path /etc/interlink/InterLinkConfig.yaml
//
// The function also processes command line flags for logging levels:
//   - --verbose: Enable debug logging
//   - --errorsonly: Show only error messages
//
// Environment variable overrides are applied after loading the YAML file:
//   - INTERLINKURL: Override InterlinkAddress
//   - SIDECARURL: Override Sidecarurl
//   - INTERLINKPORT: Override Interlinkport
//   - SIDECARPORT: Override Sidecarport
//
// Returns the loaded configuration and any error encountered.
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
