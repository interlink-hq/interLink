// Copyright © 2021 FORTH-ICS
// Copyright © 2017 The virtual-kubelet authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"net"
	"os"
	"path"
	"strconv"
	"time"

	// "k8s.io/client-go/rest"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes/scheme"
	lease "k8s.io/client-go/kubernetes/typed/coordination/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/record"

	//certificates "k8s.io/api/certificates/v1"

	"net/http"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	// "net/http"

	"github.com/sirupsen/logrus"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	logruslogger "github.com/virtual-kubelet/virtual-kubelet/log/logrus"
	"github.com/virtual-kubelet/virtual-kubelet/node"
	"github.com/virtual-kubelet/virtual-kubelet/node/api"
	"github.com/virtual-kubelet/virtual-kubelet/trace"
	"github.com/virtual-kubelet/virtual-kubelet/trace/opentelemetry"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"

	commonIL "github.com/intertwin-eu/interlink/pkg/virtualkubelet"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
)

func PodInformerFilter(node string) informers.SharedInformerOption {
	return informers.WithTweakListOptions(func(options *metav1.ListOptions) {
		options.FieldSelector = fields.OneTermEqualSelector("spec.nodeName", node).String()
	})
}

type Config struct {
	ConfigPath        string
	NodeName          string
	NodeVersion       string
	OperatingSystem   string
	InternalIP        string
	DaemonPort        int32
	KubeClusterDomain string
}

// Opts stores all the options for configuring the root virtual-kubelet command.
// It is used for setting flag values.
type Opts struct {
	ConfigPath string

	// Node name to use when creating a node in Kubernetes
	NodeName   string
	Verbose    bool
	ErrorsOnly bool
}

func initProvider(ctx context.Context) (func(context.Context) error, error) {
	res, err := resource.New(ctx,
		resource.WithAttributes(
			// the service name used to display traces in backends
			semconv.ServiceName("InterLink-VK"),
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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	flagnodename := flag.String("nodename", "", "The name of the node")
	flagpath := flag.String("configpath", "", "Path to the VK config")
	flag.Parse()

	configpath := ""
	if *flagpath != "" {
		configpath = *flagpath
	} else if os.Getenv("CONFIGPATH") != "" {
		configpath = os.Getenv("CONFIGPATH")
	} else {
		configpath = "/etc/interlink/InterLinkConfig.yaml"
	}

	nodename := ""
	if *flagnodename != "" {
		nodename = *flagnodename
	} else if os.Getenv("NODENAME") != "" {
		nodename = os.Getenv("NODENAME")
	} else {
		panic(fmt.Errorf("You must specify a Node name"))
	}

	interLinkConfig, err := commonIL.LoadConfig(configpath, nodename, ctx)
	if err != nil {
		panic(err)
	}

	logger := logrus.StandardLogger()
	if interLinkConfig.VerboseLogging {
		logger.SetLevel(logrus.DebugLevel)
	} else if interLinkConfig.ErrorsOnlyLogging {
		logger.SetLevel(logrus.ErrorLevel)
	} else {
		logger.SetLevel(logrus.InfoLevel)
	}
	log.L = logruslogger.FromLogrus(logrus.NewEntry(logger))

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

	// TODO: if token specified http.DefaultClient = ...
	// and remove reading from file

	http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}

	dport, err := strconv.ParseInt(os.Getenv("KUBELET_PORT"), 10, 32)
	if err != nil {
		log.G(ctx).Fatal(err)
	}

	cfg := Config{
		ConfigPath:      configpath,
		NodeName:        nodename,
		NodeVersion:     commonIL.KubeletVersion,
		OperatingSystem: "Linux",
		// https://github.com/liqotech/liqo/blob/d8798732002abb7452c2ff1c99b3e5098f848c93/deployments/liqo/templates/liqo-gateway-deployment.yaml#L69
		InternalIP: os.Getenv("POD_IP"),
		DaemonPort: int32(dport),
	}

	var kubecfg *rest.Config
	kubecfgFile, err := os.ReadFile(os.Getenv("KUBECONFIG"))
	if err != nil {
		if os.Getenv("KUBECONFIG") != "" {
			log.G(ctx).Debug(err)
		}
		log.G(ctx).Info("Trying InCluster configuration")

		kubecfg, err = rest.InClusterConfig()
		if err != nil {
			log.G(ctx).Fatal(err)
		}
	} else {
		log.G(ctx).Debug("Loading Kubeconfig from " + os.Getenv("KUBECONFIG"))
		clientCfg, err := clientcmd.NewClientConfigFromBytes(kubecfgFile)
		if err != nil {
			log.G(ctx).Fatal(err)
		}
		kubecfg, err = clientCfg.ClientConfig()
		if err != nil {
			log.G(ctx).Fatal(err)
		}
	}

	localClient := kubernetes.NewForConfigOrDie(kubecfg)

	nodeProvider, err := commonIL.NewProvider(
		cfg.ConfigPath,
		cfg.NodeName,
		cfg.NodeVersion,
		cfg.OperatingSystem,
		cfg.InternalIP,
		cfg.DaemonPort,
		ctx)

	if err != nil {
		log.G(ctx).Fatal(err)
	}

	nc, _ := node.NewNodeController(
		nodeProvider, nodeProvider.GetNode(), localClient.CoreV1().Nodes(),
		node.WithNodeEnableLeaseV1(
			lease.NewForConfigOrDie(kubecfg).Leases(v1.NamespaceNodeLease),
			300,
		),
	)

	go func() error {
		err = nc.Run(ctx)
		if err != nil {
			return fmt.Errorf("error running the node: %w", err)
		}
		return nil
	}()

	eb := record.NewBroadcaster()

	EventRecorder := eb.NewRecorder(scheme.Scheme, v1.EventSource{Component: path.Join(cfg.NodeName, "pod-controller")})

	resync, err := time.ParseDuration("30s")

	podInformerFactory := informers.NewSharedInformerFactoryWithOptions(
		localClient,
		resync,
		PodInformerFilter(cfg.NodeName),
	)

	scmInformerFactory := informers.NewSharedInformerFactoryWithOptions(
		localClient,
		resync,
	)

	scmInformer := scmInformerFactory.Core().V1().Secrets().Informer()
	podInformer := podInformerFactory.Core().V1().Secrets().Informer()

	podControllerConfig := node.PodControllerConfig{
		PodClient:         localClient.CoreV1(),
		Provider:          nodeProvider,
		EventRecorder:     EventRecorder,
		PodInformer:       podInformerFactory.Core().V1().Pods(),
		SecretInformer:    scmInformerFactory.Core().V1().Secrets(),
		ConfigMapInformer: scmInformerFactory.Core().V1().ConfigMaps(),
		ServiceInformer:   scmInformerFactory.Core().V1().Services(),
	}

	// stop signal for the informer
	stopper := make(chan struct{})
	defer close(stopper)

	// start informers ->
	go podInformerFactory.Start(stopper)
	go scmInformerFactory.Start(stopper)
	go scmInformer.Run(stopper)
	go podInformer.Run(stopper)

	// start to sync and call list
	if !cache.WaitForCacheSync(stopper, podInformerFactory.Core().V1().Pods().Informer().HasSynced) {
		log.G(ctx).Fatal(fmt.Errorf("timed out waiting for caches to sync"))
		return
	}

	// // DEBUG
	// lister := podInformerFactory.Core().V1().Pods().Lister().Pods("")
	// pods, err := lister.List(labels.Everything())
	// if err != nil {
	// 	fmt.Println(err)
	// }
	// for pod := range pods {
	// 	fmt.Println("pods:", pods[pod].Name)
	// }

	// start podHandler
	handlerPodConfig := api.PodHandlerConfig{
		GetContainerLogs: nodeProvider.GetLogs,
		GetPods:          nodeProvider.GetPods,
		GetStatsSummary:  nodeProvider.GetStatsSummary,
	}

	mux := http.NewServeMux()

	podRoutes := api.PodHandlerConfig{
		GetContainerLogs: handlerPodConfig.GetContainerLogs,
		GetStatsSummary:  handlerPodConfig.GetStatsSummary,
		GetPods:          handlerPodConfig.GetPods,
	}

	api.AttachPodRoutes(podRoutes, mux, true)

	//retriever, err := newCertificateRetriever(localClient, certificates.KubeletServingSignerName, cfg.NodeName, parsedIP)
	//if err != nil {
	//	log.G(ctx).Fatal("failed to initialize certificate manager: %w", err)
	//}
	// TODO: create a csr auto approver https://github.com/liqotech/liqo/blob/master/cmd/liqo-controller-manager/main.go#L498
	retriever := commonIL.NewSelfSignedCertificateRetriever(cfg.NodeName, net.ParseIP(cfg.InternalIP))

	server := &http.Server{
		Addr:              fmt.Sprintf("0.0.0.0:%d", 10250),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second, // Required to limit the effects of the Slowloris attack.
		TLSConfig: &tls.Config{
			GetCertificate:     retriever,
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: true,
		},
	}

	go func() {
		log.G(ctx).Infof("Starting the virtual kubelet HTTPs server listening on %q", server.Addr)

		// Key and certificate paths are not specified, since already configured as part of the TLSConfig.
		if err := server.ListenAndServeTLS("", ""); err != nil {
			log.G(ctx).Errorf("Failed to start the HTTPs server: %v", err)
			os.Exit(1)
		}
	}()

	pc, err := node.NewPodController(podControllerConfig) // <-- instatiates the pod controller
	if err != nil {
		log.G(ctx).Fatal(err)
	}
	err = pc.Run(ctx, 1) // <-- starts watching for pods to be scheduled on the node
	if err != nil {
		log.G(ctx).Fatal(err)
	}

}
