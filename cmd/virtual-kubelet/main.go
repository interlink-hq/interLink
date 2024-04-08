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
	"google.golang.org/grpc/credentials/insecure"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes/scheme"
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

// NewOpts returns an Opts struct with the default values set.
func NewOpts(nodename string, configpath string, config commonIL.VirtualKubeletConfig) *Opts {

	if nodename == "" {
		nodename = os.Getenv("NODENAME")
	}

	if configpath == "" {
		configpath = os.Getenv("CONFIGPATH")
	}

	return &Opts{
		ConfigPath: configpath,
		NodeName:   nodename,
		Verbose:    config.VerboseLogging,
		ErrorsOnly: config.ErrorsOnlyLogging,
	}
}

func initProvider() (func(context.Context) error, error) {
	ctx := context.Background()

	res, err := resource.New(ctx,
		resource.WithAttributes(
			// the service name used to display traces in backends
			semconv.ServiceName("InterLink-service"),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	// TODO: disable is telemetry is disabled

	// If the OpenTelemetry Collector is running on a local cluster (minikube or
	// microk8s), it should be accessible through the NodePort service at the
	// `localhost:30080` endpoint. Otherwise, replace `localhost` with the
	// endpoint of your cluster. If you run the app inside k8s, then you can
	// probably connect directly to the service through dns.
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx, "localhost:4317",
		// Note the use of insecure transport here. TLS is recommended in production.
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
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

	// Shutdown will flush any remaining spans and shut down the exporter.
	return tracerProvider.Shutdown, nil
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	nodename := flag.String("nodename", "", "The name of the node")
	configpath := flag.String("configpath", "", "Path to the VK config")
	interLinkConfig, err := commonIL.LoadConfig(*configpath, *nodename, ctx)
	if err != nil {
		panic(err)
	}
	opts := NewOpts(*nodename, *configpath, interLinkConfig)

	logger := logrus.StandardLogger()
	if interLinkConfig.VerboseLogging {
		logger.SetLevel(logrus.DebugLevel)
	} else if interLinkConfig.ErrorsOnlyLogging {
		logger.SetLevel(logrus.ErrorLevel)
	} else {
		logger.SetLevel(logrus.InfoLevel)
	}
	log.L = logruslogger.FromLogrus(logrus.NewEntry(logger))

	shutdown, err := initProvider()
	if err != nil {
		log.G(ctx).Fatal(err)
	}
	defer func() {
		if err := shutdown(ctx); err != nil {
			log.G(ctx).Fatal("failed to shutdown TracerProvider: %w", err)
		}
	}()

	log.G(ctx).Info("Tracer setup succeeded")

	// TODO: disable this through options
	trace.T = opentelemetry.Adapter{}

	// TODO: if token specified http.DefaultClient = ...
	// and remove reading from file

	http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}

	log.G(ctx).Debug(*opts)

	dport, err := strconv.ParseInt(os.Getenv("KUBELET_PORT"), 10, 32)
	if err != nil {
		log.G(ctx).Fatal(err)
	}

	cfg := Config{
		ConfigPath:      opts.ConfigPath,
		NodeName:        opts.NodeName,
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

	nodeProvider, err := commonIL.NewProvider(cfg.ConfigPath, cfg.NodeName, cfg.OperatingSystem, cfg.InternalIP, cfg.DaemonPort, ctx)
	go func() {

		ILbind := false
		retValue := -1
		counter := 0

		for {
			ILbind, retValue, err = commonIL.PingInterLink(ctx, interLinkConfig)

			if err != nil {
				log.G(ctx).Error(err)
			}

			if !ILbind && retValue == 1 {
				counter++
			} else if ILbind && retValue == 0 {
				counter = 0
			}

			if counter > 10 {
				log.G(ctx).Fatal("Unable to communicate with the InterLink API, exiting...")
			}

			time.Sleep(time.Second * 10)

		}
	}()

	if err != nil {
		log.G(ctx).Fatal(err)
	}

	nc, _ := node.NewNodeController(
		nodeProvider, nodeProvider.GetNode(), localClient.CoreV1().Nodes(),
	)

	go func() error {
		err = nc.Run(ctx)
		if err != nil {
			return fmt.Errorf("error running the node: %w", err)
		}
		return nil
	}()
	// <-nc.Ready()
	// close(nc)

	eb := record.NewBroadcaster()

	EventRecorder := eb.NewRecorder(scheme.Scheme, v1.EventSource{Component: path.Join(opts.NodeName, "pod-controller")})

	resync, err := time.ParseDuration("30s")

	podInformerFactory := informers.NewSharedInformerFactoryWithOptions(
		localClient,
		resync,
		PodInformerFilter(opts.NodeName),
	)

	scmInformerFactory := informers.NewSharedInformerFactoryWithOptions(
		localClient,
		resync,
	)

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

	parsedIP := net.ParseIP(interLinkConfig.PodIP)

	//retriever, err := newCertificateRetriever(localClient, certificates.KubeletServingSignerName, cfg.NodeName, parsedIP)
	//if err != nil {
	//	log.G(ctx).Fatal("failed to initialize certificate manager: %w", err)
	//}
	// TODO: create a csr auto approver https://github.com/liqotech/liqo/blob/master/cmd/liqo-controller-manager/main.go#L498
	retriever := commonIL.NewSelfSignedCertificateRetriever(cfg.NodeName, parsedIP)

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
