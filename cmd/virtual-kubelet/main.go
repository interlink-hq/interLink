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

// Package main implements the virtual-kubelet executable for InterLink.
//
// The Virtual Kubelet acts as a bridge between Kubernetes and external compute resources
// through the InterLink API. It creates a virtual node in the Kubernetes cluster that
// can schedule pods to remote execution environments.
//
// Key features:
//   - Creates and manages a virtual node in Kubernetes
//   - Proxies pod operations to InterLink API
//   - Handles TLS/mTLS communication
//   - Supports WebSocket tunneling for port exposure
//   - Manages pod lifecycle and status updates
//   - Provides kubelet-compatible HTTP API endpoints
//
// Usage:
//
//	virtual-kubelet -nodename <node-name> -configpath <config-file>
//
// Environment Variables:
//   - NODENAME: Name of the virtual node (required)
//   - CONFIGPATH: Path to configuration file
//   - KUBECONFIG: Path to Kubernetes configuration
//   - KUBELET_URL: Virtual kubelet HTTP server bind address
//   - KUBELET_PORT: Virtual kubelet HTTP server port
//   - ENABLE_TRACING: Enable OpenTelemetry tracing (set to "1")
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
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	// "k8s.io/client-go/rest"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes/scheme"
	lease "k8s.io/client-go/kubernetes/typed/coordination/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/record"

	// certificates "k8s.io/api/certificates/v1"

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

	"github.com/interlink-hq/interlink/pkg/interlink"
	commonIL "github.com/interlink-hq/interlink/pkg/virtualkubelet"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// UnixSocketRoundTripper is a custom RoundTripper for Unix socket connections.
// It handles the http+unix scheme by converting it to regular http for Unix domain socket communication.
type UnixSocketRoundTripper struct {
	// Transport is the underlying HTTP transport to use
	Transport http.RoundTripper
}

// RoundTrip implements the http.RoundTripper interface for Unix socket connections.
// It converts http+unix URLs to regular http URLs for Unix domain socket communication.
func (rt *UnixSocketRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.HasPrefix(req.URL.Scheme, "http+unix") {
		// Adjust the URL for Unix socket connections
		req.URL.Scheme = "http"
		req.URL.Host = "unix"
	}
	return rt.Transport.RoundTrip(req)
}

// PodInformerFilter creates a shared informer option that filters pods by node name.
// This ensures the virtual kubelet only receives events for pods scheduled on its node.
func PodInformerFilter(node string) informers.SharedInformerOption {
	return informers.WithTweakListOptions(func(options *metav1.ListOptions) {
		options.FieldSelector = fields.OneTermEqualSelector("spec.nodeName", node).String()
	})
}

// Config holds the main configuration for the virtual kubelet instance.
// It defines the node identity and connection parameters.
type Config struct {
	// ConfigPath is the path to the configuration file
	ConfigPath string
	// NodeName is the name of the virtual node in Kubernetes
	NodeName string
	// NodeVersion is the kubelet version to report
	NodeVersion string
	// OperatingSystem is the OS type to report (typically "Linux")
	OperatingSystem string
	// InternalIP is the internal IP address of the virtual node
	InternalIP string
	// DaemonPort is the port for the kubelet HTTP API server
	DaemonPort int32
	// KubeClusterDomain is the cluster domain name (optional)
	KubeClusterDomain string
}

// Opts stores all the options for configuring the root virtual-kubelet command.
// It is used for setting flag values and command-line parameters.
type Opts struct {
	// ConfigPath is the path to the configuration file
	ConfigPath string

	// NodeName is the name to use when creating a node in Kubernetes
	NodeName string
	// Verbose enables verbose logging output
	Verbose bool
	// ErrorsOnly restricts logging to error messages only
	ErrorsOnly bool
}

// parseConfiguration parses command line flags and environment variables
func parseConfiguration() (string, string) {
	flagnodename := flag.String("nodename", "", "The name of the node")
	flagpath := flag.String("configpath", "", "Path to the VK config")
	flag.Parse()

	configpath := ""
	switch {
	case *flagpath != "":
		configpath = *flagpath
	case os.Getenv("CONFIGPATH") != "":
		configpath = os.Getenv("CONFIGPATH")
	default:
		configpath = "/etc/interlink/InterLinkConfig.yaml"
	}

	nodename := ""
	switch {
	case *flagnodename != "":
		nodename = *flagnodename
	case os.Getenv("NODENAME") != "":
		nodename = os.Getenv("NODENAME")
	default:
		panic(fmt.Errorf("you must specify a Node name"))
	}

	return configpath, nodename
}

// setupLogging configures the logging system
func setupLogging(interLinkConfig commonIL.Config) {
	logger := logrus.StandardLogger()
	switch {
	case interLinkConfig.VerboseLogging:
		logger.SetLevel(logrus.DebugLevel)
	case interLinkConfig.ErrorsOnlyLogging:
		logger.SetLevel(logrus.ErrorLevel)
	default:
		logger.SetLevel(logrus.InfoLevel)
	}
	log.L = logruslogger.FromLogrus(logrus.NewEntry(logger))
}

// setupTracing initializes OpenTelemetry tracing if enabled
func setupTracing(ctx context.Context) func() {
	if os.Getenv("ENABLE_TRACING") != "1" {
		return func() {}
	}

	shutdown, err := interlink.InitTracer(ctx, "VK-InterLink-")
	if err != nil {
		log.G(ctx).Fatal(err)
	}

	log.G(ctx).Info("Tracer setup succeeded")
	trace.T = opentelemetry.Adapter{}

	return func() {
		if err = shutdown(ctx); err != nil {
			log.G(ctx).Fatal("failed to shutdown TracerProvider: %w", err)
		}
	}
}

// getKubeletEndpoint gets the kubelet URL and port from environment variables
func getKubeletEndpoint() (string, int32) {
	var kubeletURL string
	if envString, found := os.LookupEnv("KUBELET_URL"); !found {
		kubeletURL = "0.0.0.0"
	} else {
		kubeletURL = envString
	}

	var kubeletPort string
	if envString, found := os.LookupEnv("KUBELET_PORT"); !found {
		kubeletPort = "5820"
	} else {
		kubeletPort = envString
	}

	dport, err := strconv.ParseInt(kubeletPort, 10, 32)
	if err != nil {
		log.G(context.Background()).Fatal(err)
	}

	return kubeletURL, int32(dport)
}

func createCertPool(ctx context.Context, interLinkConfig commonIL.Config) *x509.CertPool {
	certPool, err := x509.SystemCertPool()
	if err != nil {
		log.G(ctx).Fatalf("Failed to parse system rootCAs for client: %v", err)
	}

	if interLinkConfig.HTTP.CaCert != "" {

		certContent, err := os.ReadFile(interLinkConfig.HTTP.CaCert)
		if err != nil {
			log.G(ctx).Fatalf("Failed to read config-provided rootCAs for client: %v", err)
		}

		certFromConfig, err := x509.ParseCertificate(certContent)
		if err != nil {
			log.G(ctx).Fatalf("Failed to parse config-provided rootCAs for client: %v", err)
		}
		certPool.AddCert(certFromConfig)
	}
	return certPool
}

// createHTTPServer creates and starts the HTTPS server
func createHTTPServer(ctx context.Context, cfg Config, interLinkConfig commonIL.Config, kubeClient kubernetes.Interface) *http.ServeMux {
	mux := http.NewServeMux()

	var retriever commonIL.Crtretriever

	switch {
	// Priority 1: Use manually provided certificate files if specified
	case interLinkConfig.KubeletCertFile != "" && interLinkConfig.KubeletKeyFile != "":
		log.G(ctx).Infof("Using manually provided certificate from %s", interLinkConfig.KubeletCertFile)
		retriever = func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			cert, err := tls.LoadX509KeyPair(interLinkConfig.KubeletCertFile, interLinkConfig.KubeletKeyFile)
			if err != nil {
				return nil, fmt.Errorf("failed to load kubelet certificate: %w", err)
			}
			return &cert, nil
		}
	// Priority 2: Use self-signed certificates if CSR is disabled
	case interLinkConfig.DisableCSR:
		log.G(ctx).Info("CSR creation is disabled in configuration, using self-signed certificates")
		retriever = commonIL.NewSelfSignedCertificateRetriever(cfg.NodeName, net.ParseIP(cfg.InternalIP))
	// Priority 3: Use CSR-based certificate retriever (default)
	default:
		// Use configured signer name, or default to standard kubelet serving signer
		kubeletServingSigner := interLinkConfig.KubeletCSRSignerName
		if kubeletServingSigner == "" {
			kubeletServingSigner = "kubernetes.io/kubelet-serving"
		}
		log.G(ctx).Infof("Using CSR signer: %s", kubeletServingSigner)

		var err error
		retriever, err = commonIL.NewCertificateRetriever(kubeClient, kubeletServingSigner, cfg.NodeName, net.ParseIP(cfg.InternalIP))
		if err != nil {
			log.G(ctx).Warnf("Failed to create CSR-based certificate retriever, falling back to self-signed: %v", err)
			retriever = commonIL.NewSelfSignedCertificateRetriever(cfg.NodeName, net.ParseIP(cfg.InternalIP))
		}
	}

	kubeletURL, _ := getKubeletEndpoint()
	server := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", kubeletURL, cfg.DaemonPort),
		Handler:           mux,
		ReadTimeout:       30 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		TLSConfig: &tls.Config{
			GetCertificate:     retriever,
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: interLinkConfig.KubeletHTTP.Insecure,
		},
	}

	go func() {
		log.G(ctx).Infof("Starting the virtual kubelet HTTPs server %q", server.Addr)
		if err := server.ListenAndServeTLS("", ""); err != nil {
			log.G(ctx).Errorf("Failed to start the HTTPs server: %v", err)
			os.Exit(1)
		}
	}()

	return mux
}

// createHTTPTransport creates the HTTP transport with TLS configuration
func createHTTPTransport(ctx context.Context, interLinkConfig commonIL.Config, vkConfig commonIL.Config) *http.Transport {
	var socketPath string
	if strings.HasPrefix(interLinkConfig.InterlinkURL, "unix://") {
		socketPath = strings.ReplaceAll(interLinkConfig.InterlinkURL, "unix://", "")
	}

	certPool := createCertPool(ctx, interLinkConfig)

	dialer := &net.Dialer{
		Timeout:   90 * time.Second,
		KeepAlive: 90 * time.Second,
	}

	// Create TLS client config with client certificates for mTLS if configured
	tlsClientConfig := &tls.Config{
		InsecureSkipVerify: interLinkConfig.HTTP.Insecure,
		RootCAs:            certPool,
		MinVersion:         tls.VersionTLS12,
	}

	// Load client certificate and key for mTLS if provided in VK config
	if vkConfig.TLS.Enabled && vkConfig.TLS.CertFile != "" && vkConfig.TLS.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(vkConfig.TLS.CertFile, vkConfig.TLS.KeyFile)
		if err != nil {
			log.G(ctx).Fatalf("Failed to load client certificate pair (%s, %s): %v", vkConfig.TLS.CertFile, vkConfig.TLS.KeyFile, err)
		}
		tlsClientConfig.Certificates = []tls.Certificate{cert}
		log.G(ctx).Info("Loaded client certificate for mTLS from: ", vkConfig.TLS.CertFile, " and ", vkConfig.TLS.KeyFile)
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
		TLSClientConfig: tlsClientConfig,
	}

	http.DefaultClient = &http.Client{
		Transport: &UnixSocketRoundTripper{
			Transport: transport,
		},
	}

	return transport
}

// setupKubernetesClient creates the Kubernetes client configuration
func setupKubernetesClient(ctx context.Context) (*rest.Config, *kubernetes.Clientset) {
	var kubecfg *rest.Config
	kubeconfigPath := os.Getenv("KUBECONFIG")
	if !filepath.IsAbs(kubeconfigPath) || strings.Contains(kubeconfigPath, "..") {
		log.G(ctx).Fatal("Invalid KUBECONFIG path")
	}
	kubecfgFile, err := os.ReadFile(kubeconfigPath) // #nosec G703
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
	return kubecfg, localClient
}

// setupInformers creates and starts the Kubernetes informers
func setupInformers(ctx context.Context, localClient *kubernetes.Clientset, nodeName string) (informers.SharedInformerFactory, informers.SharedInformerFactory, chan struct{}) {
	resync, err := time.ParseDuration("30s")
	if err != nil {
		log.G(ctx).Fatal(err)
	}

	podInformerFactory := informers.NewSharedInformerFactoryWithOptions(
		localClient,
		resync,
		PodInformerFilter(nodeName),
	)

	scmInformerFactory := informers.NewSharedInformerFactoryWithOptions(
		localClient,
		resync,
	)

	// stop signal for the informer
	stopper := make(chan struct{})

	// start informers
	go podInformerFactory.Start(stopper)
	go scmInformerFactory.Start(stopper)

	return podInformerFactory, scmInformerFactory, stopper
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	configpath, nodename := parseConfiguration()

	interLinkConfig, err := commonIL.LoadConfig(ctx, configpath)
	if err != nil {
		panic(err)
	}

	// Load Virtual Kubelet config to get TLS settings for client certificates
	vkConfig, err := commonIL.LoadConfig(ctx, configpath)
	if err != nil {
		panic(err)
	}

	setupLogging(interLinkConfig)
	log.G(ctx).Info("Config dump", interLinkConfig)

	shutdownTracing := setupTracing(ctx)
	defer shutdownTracing()

	_, dport := getKubeletEndpoint()

	cfg := Config{
		ConfigPath:      configpath,
		NodeName:        nodename,
		NodeVersion:     commonIL.KubeletVersion,
		OperatingSystem: "Linux",
		// https://github.com/liqotech/liqo/blob/d8798732002abb7452c2ff1c99b3e5098f848c93/deployments/liqo/templates/liqo-gateway-deployment.yaml#L69
		InternalIP: os.Getenv("POD_IP"),
		DaemonPort: dport,
	}

	kubecfg, localClient := setupKubernetesClient(ctx)

	mux := createHTTPServer(ctx, cfg, interLinkConfig, localClient)

	transport := createHTTPTransport(ctx, interLinkConfig, vkConfig)

	nodeProvider, err := commonIL.NewProvider(
		ctx,
		cfg.ConfigPath,
		cfg.NodeName,
		cfg.NodeVersion,
		cfg.OperatingSystem,
		cfg.InternalIP,
		cfg.DaemonPort,
		transport.Clone(),
	)
	if err != nil {
		log.G(ctx).Fatal(err)
	}

	nc, err := node.NewNodeController(
		nodeProvider, nodeProvider.GetNode(), localClient.CoreV1().Nodes(),
		node.WithNodeEnableLeaseV1(
			lease.NewForConfigOrDie(kubecfg).Leases(v1.NamespaceNodeLease),
			300,
		),
	)
	if err != nil {
		log.G(ctx).Fatalf("error setting up NodeController: %w", err)
	}

	go func() {
		err = nc.Run(ctx)
		if err != nil {
			log.G(ctx).Fatalf("error running the node: %v", err)
		}
	}()

	eb := record.NewBroadcaster()
	EventRecorder := eb.NewRecorder(scheme.Scheme, v1.EventSource{Component: path.Join(cfg.NodeName, "pod-controller")})

	podInformerFactory, scmInformerFactory, stopper := setupInformers(ctx, localClient, cfg.NodeName)
	defer close(stopper)

	podInformer := podInformerFactory.Core().V1().Pods().Informer()
	secretInformer := scmInformerFactory.Core().V1().Secrets().Informer()
	cfgInformer := scmInformerFactory.Core().V1().ConfigMaps().Informer()

	go podInformer.Run(stopper)
	go secretInformer.Run(stopper)
	go cfgInformer.Run(stopper)

	// start to sync and call list
	if !cache.WaitForCacheSync(stopper, podInformerFactory.Core().V1().Pods().Informer().HasSynced) {
		log.G(ctx).Fatal(fmt.Errorf("timed out waiting for caches to sync"))
	}

	podControllerConfig := node.PodControllerConfig{
		PodClient:                 localClient.CoreV1(),
		EventRecorder:             EventRecorder,
		Provider:                  nodeProvider,
		PodInformer:               podInformerFactory.Core().V1().Pods(),
		SecretInformer:            scmInformerFactory.Core().V1().Secrets(),
		ConfigMapInformer:         scmInformerFactory.Core().V1().ConfigMaps(),
		ServiceInformer:           scmInformerFactory.Core().V1().Services(),
		SkipDownwardAPIResolution: interLinkConfig.SkipDownwardAPIResolution, // set to true to skip downward API resolution
	}

	log.G(ctx).Info("Before running the PVC placeholder controller")
	go runPVCPlaceholderController(ctx, localClient, cfg.NodeName)

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

	podRoutes := api.PodHandlerConfig{
		GetContainerLogs: handlerPodConfig.GetContainerLogs,
		GetStatsSummary:  handlerPodConfig.GetStatsSummary,
		GetPods:          handlerPodConfig.GetPods,
	}

	api.AttachPodRoutes(podRoutes, mux, true)

	pc, err := node.NewPodController(podControllerConfig)
	if err != nil {
		log.G(ctx).Fatal(err)
	}
	err = pc.Run(ctx, 1)
	if err != nil {
		log.G(ctx).Fatal(err)
	}
}

func runPVCPlaceholderController(ctx context.Context, client kubernetes.Interface, nodeName string) {

	log.G(ctx).Info("Starting PVC placeholder controller")

	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:

			namespaces, err := client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
			if err != nil {
				log.G(ctx).Error("Error listing namespaces: ", err)
				continue
			}

			for _, ns := range namespaces.Items {

				if ns.Name == "kube-system" {
					log.G(ctx).Debugf("Skipping kube-system namespace")
					continue
				}
				if ns.Name == "default" {
					log.G(ctx).Debugf("Skipping default namespace")
					continue
				}

				podList, err := client.CoreV1().Pods(ns.Name).List(ctx, metav1.ListOptions{})
				if err != nil {
					log.G(ctx).Error("Error listing pods: ", err)
					continue
				}

				var matchingPods []v1.Pod
				for _, pod := range podList.Items {
					if pod.Spec.NodeSelector != nil {
						if val, ok := pod.Spec.NodeSelector["kubernetes.io/hostname"]; ok && val == nodeName {
							matchingPods = append(matchingPods, pod)
						}
					}
				}

				if len(matchingPods) == 0 {
					log.G(ctx).Info("No pods with nodeSelector kubernetes.io/hostname=" + nodeName + " found in namespace " + ns.Name)
				} else {
					log.G(ctx).Infof("Found %d pods with nodeSelector kubernetes.io/hostname=%s in namespace %s", len(matchingPods), nodeName, ns.Name)
				}

				log.G(ctx).Debugf("Pods in namespace %s: %d", ns.Name, len(matchingPods))

				if err != nil {
					log.G(ctx).Error("Error listing pods: ", err)
					continue
				}
				for _, pod := range matchingPods {

					log.G(ctx).Debugf("Checking pod %s in namespace %s", pod.Name, pod.Namespace)

					if !hasVirtualNodeToleration(&pod) {
						log.G(ctx).Debugf("Pod %s does not have the virtual node toleration, skipping", pod.Name)
						continue
					}

					if pod.Status.Phase != v1.PodPending {
						log.G(ctx).Debugf("Pod %s is not in Pending state, skipping", pod.Name)
						continue
					}

					for _, vol := range pod.Spec.Volumes {
						if vol.PersistentVolumeClaim != nil {

							log.G(ctx).Debugf("Checking PV %s for pod %s", vol.PersistentVolumeClaim.ClaimName, pod.Name)
							_, err := client.CoreV1().PersistentVolumes().Get(ctx, vol.PersistentVolumeClaim.ClaimName, metav1.GetOptions{})
							if err != nil {
								log.G(ctx).Debugf("PV %s not found for pod %s", vol.PersistentVolumeClaim.ClaimName, pod.Name)
								if apierrors.IsNotFound(err) {
									log.G(ctx).Infof("PV %s not found for pod %s, creating placeholder", vol.PersistentVolumeClaim.ClaimName, pod.Name)
									_, err = createTempPv(ctx, client, &pod, vol)
									if err != nil {
										log.G(ctx).Error("Failed to create placeholder PV for pod ", pod.Name, ": ", err)
									}
								} else {
									log.G(ctx).Errorf("Error getting PV %s for pod %s: %v", vol.PersistentVolumeClaim.ClaimName, pod.Name, err)
								}
							}

							log.G(ctx).Debugf("Checking PVC %s for pod %s", vol.PersistentVolumeClaim.ClaimName, pod.Name)
							_, err = client.CoreV1().PersistentVolumeClaims(pod.Namespace).Get(ctx, vol.PersistentVolumeClaim.ClaimName, metav1.GetOptions{})
							if err != nil {
								log.G(ctx).Debugf("PVC %s not found for pod %s", vol.PersistentVolumeClaim.ClaimName, pod.Name)
								if apierrors.IsNotFound(err) {
									log.G(ctx).Infof("PVC %s not found for pod %s, creating placeholder", vol.PersistentVolumeClaim.ClaimName, pod.Name)
									_, err = createTempPVC(ctx, client, &pod, vol)
									if err != nil {
										log.G(ctx).Error("Failed to create placeholder PVC for pod ", pod.Name, ": ", err)
									}
								} else {
									log.G(ctx).Errorf("Error getting PVC %s for pod %s: %v", vol.PersistentVolumeClaim.ClaimName, pod.Name, err)
								}
							}
						}
					}
				}
			}
		}
	}
}

func hasVirtualNodeToleration(pod *v1.Pod) bool {
	for _, tol := range pod.Spec.Tolerations {
		if tol.Key == "virtual-node.interlink/no-schedule" && tol.Operator == v1.TolerationOpExists {
			return true
		}
	}
	return false
}

func createTempPVC(ctx context.Context, client kubernetes.Interface, pod *v1.Pod, volume v1.Volume) (*v1.PersistentVolumeClaim, error) {
	tempPVC := &v1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      volume.PersistentVolumeClaim.ClaimName,
			Namespace: pod.Namespace,
			Labels: map[string]string{
				"temporary": "true",
			},
		},
		Spec: v1.PersistentVolumeClaimSpec{
			AccessModes: []v1.PersistentVolumeAccessMode{
				v1.ReadWriteOnce,
			},
			Resources: v1.VolumeResourceRequirements{
				Requests: v1.ResourceList{
					"storage": resource.MustParse("1Gi"),
				},
			},
		},
	}

	createdPVC, err := client.CoreV1().PersistentVolumeClaims(pod.Namespace).Create(ctx, tempPVC, metav1.CreateOptions{})
	if err != nil {
		log.G(ctx).Error("failed to create temporary PVC: ", err)
		return nil, err
	}

	log.G(ctx).Infof("Temporary PVC %s created for pod %s", createdPVC.Name, pod.Name)

	return createdPVC, nil
}

func createTempPv(ctx context.Context, client kubernetes.Interface, pod *v1.Pod, volume v1.Volume) (*v1.PersistentVolume, error) {

	pvName := "temp-pv-" + volume.PersistentVolumeClaim.ClaimName
	tempPV := &v1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: pvName,
			Labels: map[string]string{
				"temporary": "true",
			},
		},
		Spec: v1.PersistentVolumeSpec{
			Capacity: v1.ResourceList{
				"storage": resource.MustParse("1Gi"),
			},
			AccessModes: []v1.PersistentVolumeAccessMode{
				v1.ReadWriteOnce,
			},
			PersistentVolumeReclaimPolicy: v1.PersistentVolumeReclaimDelete,
			PersistentVolumeSource: v1.PersistentVolumeSource{
				HostPath: &v1.HostPathVolumeSource{
					Path: "/tmp/" + volume.PersistentVolumeClaim.ClaimName,
				},
			},
			ClaimRef: &v1.ObjectReference{
				Kind:      "PersistentVolumeClaim",
				Namespace: pod.Namespace,
				Name:      volume.PersistentVolumeClaim.ClaimName,
			},
		},
	}

	createdPV, err := client.CoreV1().PersistentVolumes().Create(ctx, tempPV, metav1.CreateOptions{})
	if err != nil {
		log.G(ctx).Infof("failed to create temporary PV: ", err)
		return nil, err
	}

	log.G(ctx).Infof("Temporary PV %s created for PVC %s", pvName, createdPV.Name)

	return createdPV, nil
}
