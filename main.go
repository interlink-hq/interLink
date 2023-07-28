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
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path"
	"time"

	//"k8s.io/client-go/rest"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/record"

	"net/http"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	//"net/http"

	"github.com/intertwin-eu/interlink/pkg/virtualkubelet"
	"github.com/sirupsen/logrus"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	logruslogger "github.com/virtual-kubelet/virtual-kubelet/log/logrus"
	"github.com/virtual-kubelet/virtual-kubelet/node"
	"github.com/virtual-kubelet/virtual-kubelet/node/api"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	stats "k8s.io/kubernetes/pkg/kubelet/apis/stats/v1alpha1"
)

func PodInformerFilter(node string) informers.SharedInformerOption {
	return informers.WithTweakListOptions(func(options *metav1.ListOptions) {
		options.FieldSelector = fields.OneTermEqualSelector("spec.nodeName", node).String()
	})
}

type PodHandler interface {
	// List returns the list of reflected pods.
	List(context.Context) ([]*v1.Pod, error)
	// Exec executes a command in a container of a reflected pod.
	Exec(ctx context.Context, namespace, pod, container string, cmd []string, attach api.AttachIO) error
	// Attach attaches to a process that is already running inside an existing container of a reflected pod.
	Attach(ctx context.Context, namespace, pod, container string, attach api.AttachIO) error
	// PortForward forwards a connection from local to the ports of a reflected pod.
	PortForward(ctx context.Context, namespace, pod string, port int32, stream io.ReadWriteCloser) error
	// Logs retrieves the logs of a container of a reflected pod.
	Logs(ctx context.Context, namespace, pod, container string, opts api.ContainerLogOpts) (io.ReadCloser, error)
	// Stats retrieves the stats of the reflected pods.
	Stats(ctx context.Context) (*stats.Summary, error)
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
	NodeName string
}

// NewOpts returns an Opts struct with the default values set.
func NewOpts() *Opts {
	return &Opts{
		ConfigPath: os.Getenv("CONFIGPATH"),
		NodeName:   os.Getenv("NODENAME"),
	}
}

func main() {
	// Try from bash:
	// INTERLINKCONFIGPATH=$PWD/kustomizations/InterLinkConfig.yaml CONFIGPATH=$PWD/kustomizations/knoc-cfg.json NODENAME=test-vk ./bin/vk

	http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger := logrus.StandardLogger()
	logger.SetLevel(logrus.DebugLevel)
	log.L = logruslogger.FromLogrus(logrus.NewEntry(logger))

	opts := NewOpts()

	cfg := Config{
		ConfigPath:      opts.ConfigPath,
		NodeName:        opts.NodeName,
		OperatingSystem: "Linux",
		// https://github.com/liqotech/liqo/blob/d8798732002abb7452c2ff1c99b3e5098f848c93/deployments/liqo/templates/liqo-gateway-deployment.yaml#L69
		InternalIP: "127.0.0.1",
		DaemonPort: 10250,
	}

	kubecfgFile, err := ioutil.ReadFile(os.Getenv("KUBECONFIG"))
	if err != nil {
		log.G(ctx).Fatal(err)
	}

	clientCfg, err := clientcmd.NewClientConfigFromBytes(kubecfgFile)
	if err != nil {
		log.G(ctx).Fatal(err)
	}

	var kubecfg *rest.Config

	kubecfg, err = clientCfg.ClientConfig()
	if err != nil {
		log.G(ctx).Fatal(err)
	}

	// TODO: enable on demand
	// kubecfg, err := rest.InClusterConfig()
	// if err != nil {
	//	log.G(ctx).Fatal(err)
	// }

	localClient := kubernetes.NewForConfigOrDie(kubecfg)

	nodeProvider, err := virtualkubelet.NewProvider(cfg.ConfigPath, cfg.NodeName, cfg.OperatingSystem, cfg.InternalIP, cfg.DaemonPort, ctx)
	if err != nil {
		log.G(ctx).Fatal(err)
	}

	nc, _ := node.NewNodeController(
		nodeProvider, nodeProvider.GetNode(), localClient.CoreV1().Nodes(),
	)

	// // https://github.com/liqotech/liqo/blob/master/cmd/virtual-kubelet/root/root.go#L195C49-L195C49
	// // https://github.com/liqotech/liqo/blob/master/cmd/virtual-kubelet/root/http.go#L76-L84
	// // https://github.com/liqotech/liqo/blob/master/cmd/virtual-kubelet/root/http.go#L93

	// handler := PodHandler{
	// 	Stats: nc.GetStatsSummary
	// }

	// podRoutes := api.PodHandlerConfig{
	// 	// RunInContainer:        handler.Exec,
	// 	// AttachToContainer:     handler.Attach,
	// 	// PortForward:           handler.PortForward,
	// 	// GetContainerLogs:      handler.Logs,
	// 	GetStatsSummary:       handler.Stats,
	// 	// GetPodsFromKubernetes: handler.List,
	// 	// GetPods:               handler.List,
	// }

	// err = setupHTTPServer(ctx, podProvider.PodHandler(), localClient, remoteConfig, c)
	// if err != nil {
	// log.G(ctx).Fatal(err)
	// }

	go func() error {
		err = nc.Run(ctx)
		if err != nil {
			return fmt.Errorf("error running the node: %w", err)
		}
		return nil
	}()
	//<-nc.Ready()
	//close(nc)

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

	pc, err := node.NewPodController(podControllerConfig) // <-- instatiates the pod controller
	if err != nil {
		log.G(ctx).Fatal(err)
	}
	err = pc.Run(ctx, 1) // <-- starts watching for pods to be scheduled on the node
	if err != nil {
		log.G(ctx).Fatal(err)
	}

}
