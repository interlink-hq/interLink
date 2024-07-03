package virtualkubelet

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"os"
	"strconv"
	"time"

	"gopkg.in/yaml.v2"

	"github.com/containerd/containerd/log"
	"github.com/virtual-kubelet/virtual-kubelet/errdefs"
	"github.com/virtual-kubelet/virtual-kubelet/node/api"
	stats "github.com/virtual-kubelet/virtual-kubelet/node/api/statsv1alpha1"
	"github.com/virtual-kubelet/virtual-kubelet/trace"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	commonIL "github.com/intertwin-eu/interlink/pkg/interlink"
)

const (
	DefaultCPUCapacity    = "100"
	DefaultMemoryCapacity = "3000G"
	DefaultPodCapacity    = "10000"
	DefaultGPUCapacity    = "0"
	DefaultListenPort     = 10250
	NamespaceKey          = "namespace"
	NameKey               = "name"
	CREATE                = 0
	DELETE                = 1
)

func buildKeyFromNames(namespace string, name string) (string, error) {
	return fmt.Sprintf("%s-%s", namespace, name), nil
}

func buildKey(pod *v1.Pod) (string, error) {
	if pod.Namespace == "" {
		return "", fmt.Errorf("pod namespace not found")
	}

	if pod.Name == "" {
		return "", fmt.Errorf("pod name not found")
	}

	return buildKeyFromNames(pod.Namespace, pod.Name)
}

// VirtualKubeletProvider defines the properties of the virtual kubelet provider
type VirtualKubeletProvider struct {
	nodeName             string
	node                 *v1.Node
	operatingSystem      string
	internalIP           string
	daemonEndpointPort   int32
	pods                 map[string]*v1.Pod
	config               VirtualKubeletConfig
	startTime            time.Time
	notifier             func(*v1.Pod)
	onNodeChangeCallback func(*v1.Node)
	clientSet            *kubernetes.Clientset
}

// NewProviderConfig takes user-defined configuration and fills the Virtual Kubelet provider struct
func NewProviderConfig(
	config VirtualKubeletConfig,
	nodeName string,
	nodeVersion string,
	operatingSystem string,
	internalIP string,
	daemonEndpointPort int32,
) (*VirtualKubeletProvider, error) {

	// set defaults
	if config.CPU == "" {
		config.CPU = DefaultCPUCapacity
	}
	if config.Memory == "" {
		config.Memory = DefaultMemoryCapacity
	}
	if config.Pods == "" {
		config.Pods = DefaultPodCapacity
	}
	if config.GPU == "" {
		config.GPU = DefaultGPUCapacity
	}

	lbls := map[string]string{
		"alpha.service-controller.kubernetes.io/exclude-balancer": "true",
		"beta.kubernetes.io/os":                                   "linux",
		"kubernetes.io/hostname":                                  nodeName,
		"kubernetes.io/role":                                      "agent",
		"node.kubernetes.io/exclude-from-external-load-balancers": "true",
		"type": "virtual-kubelet",
	}

	node := v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   nodeName,
			Labels: lbls,
			//Annotations: cfg.ExtraAnnotations,
		},
		Spec: v1.NodeSpec{
			Taints: []v1.Taint{{
				Key:    "virtual-node.interlink/no-schedule",
				Value:  strconv.FormatBool(true),
				Effect: v1.TaintEffectNoSchedule,
			}},
		},
		Status: v1.NodeStatus{
			NodeInfo: v1.NodeSystemInfo{
				KubeletVersion:  nodeVersion,
				Architecture:    "virtual-kubelet",
				OperatingSystem: "linux",
			},
			Addresses:       []v1.NodeAddress{{Type: v1.NodeInternalIP, Address: internalIP}},
			DaemonEndpoints: v1.NodeDaemonEndpoints{KubeletEndpoint: v1.DaemonEndpoint{Port: int32(daemonEndpointPort)}},
			Capacity: v1.ResourceList{
				"cpu":            resource.MustParse(config.CPU),
				"memory":         resource.MustParse(config.Memory),
				"pods":           resource.MustParse(config.Pods),
				"nvidia.com/gpu": resource.MustParse(config.GPU),
			},
			Allocatable: v1.ResourceList{
				"cpu":            resource.MustParse(config.CPU),
				"memory":         resource.MustParse(config.Memory),
				"pods":           resource.MustParse(config.Pods),
				"nvidia.com/gpu": resource.MustParse(config.GPU),
			},
			Conditions: nodeConditions(),
		},
	}

	provider := VirtualKubeletProvider{
		nodeName:           nodeName,
		node:               &node,
		operatingSystem:    operatingSystem,
		internalIP:         internalIP,
		daemonEndpointPort: daemonEndpointPort,
		pods:               make(map[string]*v1.Pod),
		config:             config,
		startTime:          time.Now(),
	}

	return &provider, nil
}

// NewProvider creates a new Provider, which implements the PodNotifier and other virtual-kubelet interfaces
func NewProvider(providerConfig, nodeName, nodeVersion, operatingSystem string, internalIP string, daemonEndpointPort int32, ctx context.Context) (*VirtualKubeletProvider, error) {
	config, err := LoadConfig(providerConfig, nodeName, ctx)
	if err != nil {
		return nil, err
	}
	return NewProviderConfig(config, nodeName, nodeVersion, operatingSystem, internalIP, daemonEndpointPort)
}

// LoadConfig loads the given json configuration files and return a VirtualKubeletConfig struct
func LoadConfig(providerConfig, nodeName string, ctx context.Context) (config VirtualKubeletConfig, err error) {

	log.G(ctx).Info("Loading Virtual Kubelet config from " + providerConfig)
	data, err := os.ReadFile(providerConfig)
	if err != nil {
		return config, err
	}

	config = VirtualKubeletConfig{}
	err = yaml.Unmarshal(data, &config)

	if err != nil {
		return config, err
	}

	//config = configMap
	if config.CPU == "" {
		config.CPU = DefaultCPUCapacity
	}
	if config.Memory == "" {
		config.Memory = DefaultMemoryCapacity
	}
	if config.Pods == "" {
		config.Pods = DefaultPodCapacity
	}
	if config.GPU == "" {
		config.GPU = DefaultGPUCapacity
	}

	if _, err = resource.ParseQuantity(config.CPU); err != nil {
		return config, fmt.Errorf("invalid CPU value %v", config.CPU)
	}
	if _, err = resource.ParseQuantity(config.Memory); err != nil {
		return config, fmt.Errorf("invalid memory value %v", config.Memory)
	}
	if _, err = resource.ParseQuantity(config.Pods); err != nil {
		return config, fmt.Errorf("invalid pods value %v", config.Pods)
	}
	if _, err = resource.ParseQuantity(config.GPU); err != nil {
		return config, fmt.Errorf("invalid GPU value %v", config.GPU)
	}
	return config, nil
}

// GetNode return the Node information at the initiation of a virtual node
func (p *VirtualKubeletProvider) GetNode() *v1.Node {
	return p.node
}

// NotifyNodeStatus runs once at initiation time and set the function to be used for node change notification (native of vk)
// it also starts a go routine for continously checking the node status and availability
func (p *VirtualKubeletProvider) NotifyNodeStatus(ctx context.Context, f func(*v1.Node)) {
	p.onNodeChangeCallback = f
	go p.nodeUpdate(ctx)
}

// nodeUpdate continously checks for node status and availability
func (p *VirtualKubeletProvider) nodeUpdate(ctx context.Context) {

	t := time.NewTimer(5 * time.Second)
	if !t.Stop() {
		<-t.C
	}

	log.G(ctx).Info("nodeLoop")

	_, err := os.ReadFile(p.config.VKTokenFile) // just pass the file name
	if err != nil {
		log.G(context.Background()).Fatal(err)
	}

	for {
		t.Reset(30 * time.Second)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		bool, code, err := PingInterLink(ctx, p.config)
		if err != nil || !bool {
			p.node.Status.Conditions = []v1.NodeCondition{
				{
					Type:               "Ready",
					Status:             v1.ConditionFalse,
					LastHeartbeatTime:  metav1.Now(),
					LastTransitionTime: metav1.Now(),
					Reason:             "KubeletPending",
					Message:            "kubelet is pending.",
				},
				{
					Type:               "OutOfDisk",
					Status:             v1.ConditionFalse,
					LastHeartbeatTime:  metav1.Now(),
					LastTransitionTime: metav1.Now(),
					Reason:             "KubeletHasSufficientDisk",
					Message:            "kubelet has sufficient disk space available",
				},
				{
					Type:               "MemoryPressure",
					Status:             v1.ConditionFalse,
					LastHeartbeatTime:  metav1.Now(),
					LastTransitionTime: metav1.Now(),
					Reason:             "KubeletHasSufficientMemory",
					Message:            "kubelet has sufficient memory available",
				},
				{
					Type:               "DiskPressure",
					Status:             v1.ConditionFalse,
					LastHeartbeatTime:  metav1.Now(),
					LastTransitionTime: metav1.Now(),
					Reason:             "KubeletHasNoDiskPressure",
					Message:            "kubelet has no disk pressure",
				},
				{
					Type:               "NetworkUnavailable",
					Status:             v1.ConditionTrue,
					LastHeartbeatTime:  metav1.Now(),
					LastTransitionTime: metav1.Now(),
					Reason:             "RouteCreated",
					Message:            "RouteController created a route",
				},
			}
			p.onNodeChangeCallback(p.node)
			log.G(ctx).Error("Ping Failed with exit code: ", code)
		} else {

			p.node.Status.Conditions = []v1.NodeCondition{
				{
					Type:               "Ready",
					Status:             v1.ConditionTrue,
					LastHeartbeatTime:  metav1.Now(),
					LastTransitionTime: metav1.Now(),
					Reason:             "KubeletPending",
					Message:            "kubelet is pending.",
				},
				{
					Type:               "OutOfDisk",
					Status:             v1.ConditionFalse,
					LastHeartbeatTime:  metav1.Now(),
					LastTransitionTime: metav1.Now(),
					Reason:             "KubeletHasSufficientDisk",
					Message:            "kubelet has sufficient disk space available",
				},
				{
					Type:               "MemoryPressure",
					Status:             v1.ConditionFalse,
					LastHeartbeatTime:  metav1.Now(),
					LastTransitionTime: metav1.Now(),
					Reason:             "KubeletHasSufficientMemory",
					Message:            "kubelet has sufficient memory available",
				},
				{
					Type:               "DiskPressure",
					Status:             v1.ConditionFalse,
					LastHeartbeatTime:  metav1.Now(),
					LastTransitionTime: metav1.Now(),
					Reason:             "KubeletHasNoDiskPressure",
					Message:            "kubelet has no disk pressure",
				},
				{
					Type:               "NetworkUnavailable",
					Status:             v1.ConditionFalse,
					LastHeartbeatTime:  metav1.Now(),
					LastTransitionTime: metav1.Now(),
					Reason:             "RouteCreated",
					Message:            "RouteController created a route",
				},
			}

			log.G(ctx).Info("Ping succeded with exit code: ", code)
			p.onNodeChangeCallback(p.node)
		}
		log.G(ctx).Info("endNodeLoop")
	}

}

// Ping the kubelet from the cluster, this will always be ok by design probably
func (p *VirtualKubeletProvider) Ping(ctx context.Context) error {
	return nil
}

// CreatePod accepts a Pod definition and stores it in memory in p.pods
func (p *VirtualKubeletProvider) CreatePod(ctx context.Context, pod *v1.Pod) error {
	ctx, span := trace.StartSpan(ctx, "CreatePod")
	var hasInitContainers = false
	var state v1.ContainerState
	defer span.End()

	// Add the pod's coordinates to the current span.
	ctx = addAttributes(ctx, span, NamespaceKey, pod.Namespace, NameKey, pod.Name)
	key, err := buildKey(pod)
	if err != nil {
		return err
	}
	now := metav1.NewTime(time.Now())
	runningState := v1.ContainerState{
		Running: &v1.ContainerStateRunning{
			StartedAt: now,
		},
	}
	waitingState := v1.ContainerState{
		Waiting: &v1.ContainerStateWaiting{
			Reason: "Waiting for InitContainers",
		},
	}
	state = runningState

	// in case we have initContainers we need to stop main containers from executing for now ...
	if len(pod.Spec.InitContainers) > 0 {
		state = waitingState
		hasInitContainers = true

		// we put the phase in running but initialization phase to false
		pod.Status = v1.PodStatus{
			Phase:     v1.PodRunning,
			HostIP:    p.internalIP,
			PodIP:     p.internalIP,
			StartTime: &now,
			Conditions: []v1.PodCondition{
				{
					Type:   v1.PodInitialized,
					Status: v1.ConditionFalse,
				},
				{
					Type:   v1.PodReady,
					Status: v1.ConditionFalse,
				},
				{
					Type:   v1.PodScheduled,
					Status: v1.ConditionTrue,
				},
			},
		}
	} else {

		// if no init containers are there, go head and set phase to initialized
		pod.Status = v1.PodStatus{
			Phase:     v1.PodPending,
			HostIP:    p.internalIP,
			PodIP:     p.internalIP,
			StartTime: &now,
			Conditions: []v1.PodCondition{
				{
					Type:   v1.PodInitialized,
					Status: v1.ConditionTrue,
				},
				{
					Type:   v1.PodReady,
					Status: v1.ConditionTrue,
				},
				{
					Type:   v1.PodScheduled,
					Status: v1.ConditionTrue,
				},
			},
		}
	}

	// Create pod asynchronously on the remote plugin
	// we don't care, the statusLoop will eventually reconcile the status
	go func() {
		err := RemoteExecution(ctx, p.config, p, pod, CREATE)
		if err != nil {
			if err.Error() == "Deleted pod before actual creation" {
				log.G(ctx).Warn(err)
			} else {
				log.G(ctx).Error(err)
				pod.Status = v1.PodStatus{
					Phase:     v1.PodFailed,
					HostIP:    p.internalIP,
					PodIP:     p.internalIP,
					StartTime: &now,
					Conditions: []v1.PodCondition{
						{
							Type:   v1.PodInitialized,
							Status: v1.ConditionFalse,
						},
						{
							Type:   v1.PodReady,
							Status: v1.ConditionFalse,
						},
						{
							Type:   v1.PodScheduled,
							Status: v1.ConditionFalse,
						},
					},
				}

				p.UpdatePod(ctx, pod)

			}
			return
		}
	}()

	// set pod containers status to notReady and waiting if there is an initContainer to be executed first
	for _, container := range pod.Spec.Containers {

		pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, v1.ContainerStatus{
			Name:         container.Name,
			Image:        container.Image,
			Ready:        !hasInitContainers,
			RestartCount: 0,
			State:        state,
		})

	}

	p.pods[key] = pod

	return nil
}

// UpdatePod accepts a Pod definition and updates its reference.
func (p *VirtualKubeletProvider) UpdatePod(ctx context.Context, pod *v1.Pod) error {
	ctx, span := trace.StartSpan(ctx, "UpdatePod")
	defer span.End()

	// Add the pod's coordinates to the current span.
	ctx = addAttributes(ctx, span, NamespaceKey, pod.Namespace, NameKey, pod.Name)

	log.G(ctx).Infof("receive UpdatePod %q", pod.Name)

	p.notifier(pod)

	return nil
}

// DeletePod deletes the specified pod and drops it out of p.pods
func (p *VirtualKubeletProvider) DeletePod(ctx context.Context, pod *v1.Pod) (err error) {
	ctx, span := trace.StartSpan(ctx, "DeletePod")
	defer span.End()

	// Add the pod's coordinates to the current span.
	ctx = addAttributes(ctx, span, NamespaceKey, pod.Namespace, NameKey, pod.Name)

	log.G(ctx).Infof("receive DeletePod %q", pod.Name)

	key, err := buildKey(pod)
	if err != nil {
		return err
	}

	if _, exists := p.pods[key]; !exists {
		return errdefs.NotFound("pod not found")
	}

	now := metav1.Now()
	pod.Status.Reason = "VKProviderPodDeleted"

	go func() {
		err = RemoteExecution(ctx, p.config, p, pod, DELETE)
		if err != nil {
			log.G(ctx).Error(err)
			return
		}
	}()

	for idx := range pod.Status.ContainerStatuses {
		pod.Status.ContainerStatuses[idx].Ready = false
		pod.Status.ContainerStatuses[idx].State = v1.ContainerState{
			Terminated: &v1.ContainerStateTerminated{
				Message:    "VK provider terminated container upon deletion",
				FinishedAt: now,
				Reason:     "VKProviderPodContainerDeleted",
			},
		}
	}
	for idx := range pod.Status.InitContainerStatuses {
		pod.Status.InitContainerStatuses[idx].Ready = false
		pod.Status.InitContainerStatuses[idx].State = v1.ContainerState{
			Terminated: &v1.ContainerStateTerminated{
				Message:    "VK provider terminated container upon deletion",
				FinishedAt: now,
				Reason:     "VKProviderPodContainerDeleted",
			},
		}
	}

	// tell k8s it's terminated
	p.UpdatePod(ctx, pod)

	// delete from p.pods
	delete(p.pods, key)

	return nil
}

// GetPod returns a pod by name that is stored in memory.
func (p *VirtualKubeletProvider) GetPod(ctx context.Context, namespace, name string) (pod *v1.Pod, err error) {

	ctx, span := trace.StartSpan(ctx, "GetPod")
	defer func() {
		span.SetStatus(err)
		span.End()
	}()

	// Add the pod's coordinates to the current span.
	ctx = addAttributes(ctx, span, NamespaceKey, namespace, NameKey, name)

	log.G(ctx).Infof("receive GetPod %q", name)

	key, err := buildKeyFromNames(namespace, name)
	if err != nil {
		return nil, err
	}

	if pod, ok := p.pods[key]; ok {
		return pod, nil
	}
	return nil, errdefs.NotFoundf("pod \"%s/%s\" is not known to the provider", namespace, name)
}

// GetPodStatus returns the status of a pod by name that is "running".
// returns nil if a pod by that name is not found.
func (p *VirtualKubeletProvider) GetPodStatus(ctx context.Context, namespace, name string) (*v1.PodStatus, error) {
	ctx, span := trace.StartSpan(ctx, "GetPodStatus")
	defer span.End()

	// Add namespace and name as attributes to the current span.
	ctx = addAttributes(ctx, span, NamespaceKey, namespace, NameKey, name)

	log.G(ctx).Infof("receive GetPodStatus %q", name)

	pod, err := p.GetPod(ctx, namespace, name)
	if err != nil {
		return nil, err
	}

	return &pod.Status, nil
}

// GetPods returns a list of all pods known to be "running".
func (p *VirtualKubeletProvider) GetPods(ctx context.Context) ([]*v1.Pod, error) {
	ctx, span := trace.StartSpan(ctx, "GetPods")
	defer span.End()

	log.G(ctx).Info("receive GetPods")

	p.initClientSet(ctx)
	p.RetrievePodsFromCluster(ctx)

	var pods []*v1.Pod

	for _, pod := range p.pods {
		pods = append(pods, pod)
	}

	return pods, nil
}

// NodeConditions returns a list of conditions (Ready, OutOfDisk, etc), for updates to the node status
// within Kubernetes.
// TODO: use as var not function
func nodeConditions() []v1.NodeCondition {
	return []v1.NodeCondition{
		{
			Type:               "Ready",
			Status:             v1.ConditionTrue,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletPending",
			Message:            "kubelet is pending.",
		},
		{
			Type:               "OutOfDisk",
			Status:             v1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletHasSufficientDisk",
			Message:            "kubelet has sufficient disk space available",
		},
		{
			Type:               "MemoryPressure",
			Status:             v1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletHasSufficientMemory",
			Message:            "kubelet has sufficient memory available",
		},
		{
			Type:               "DiskPressure",
			Status:             v1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletHasNoDiskPressure",
			Message:            "kubelet has no disk pressure",
		},
		{
			Type:               "NetworkUnavailable",
			Status:             v1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "RouteCreated",
			Message:            "RouteController created a route",
		},
	}

}

// NotifyPods is called to set a pod notifier callback function. Also starts the go routine to monitor all vk pods
func (p *VirtualKubeletProvider) NotifyPods(ctx context.Context, f func(*v1.Pod)) {
	p.notifier = f
	go p.statusLoop(ctx)
}

// statusLoop preiodically monitoring the status of all the pods in p.pods
func (p *VirtualKubeletProvider) statusLoop(ctx context.Context) {
	t := time.NewTimer(5 * time.Second)
	if !t.Stop() {
		<-t.C
	}

	_, err := os.ReadFile(p.config.VKTokenFile) // just pass the file name
	if err != nil {
		log.G(context.Background()).Fatal(err)
	}

	for {
		log.G(ctx).Info("statusLoop")
		t.Reset(5 * time.Second)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}

		b, err := os.ReadFile(p.config.VKTokenFile) // just pass the file name
		if err != nil {
			fmt.Print(err)
		}

		var podsList []*v1.Pod
		for _, pod := range p.pods {
			if pod.Status.Phase != "Initializing" {
				podsList = append(podsList, pod)
				err = p.UpdatePod(ctx, pod)
				if err != nil {
					log.G(ctx).Error(err)
				}
			}
		}

		if len(podsList) > 0 {
			_, err = checkPodsStatus(ctx, p, podsList, string(b), p.config)
			if err != nil {
				log.G(ctx).Error(err)
			}
			for _, pod := range p.pods {
				key, err := buildKey(pod)
				if err != nil {
					log.G(ctx).Error(err)
				}
				p.pods[key] = pod
			}
		} else {
			log.G(ctx).Info("No pods to monitor, waiting for the next loop to start")
		}

		log.G(ctx).Info("statusLoop=end")
	}
}

// addAttributes adds the specified attributes to the provided span.
// attrs must be an even-sized list of string arguments.
// Otherwise, the span won't be modified.
// TODO: Refactor and move to a "tracing utilities" package.
func addAttributes(ctx context.Context, span trace.Span, attrs ...string) context.Context {
	if len(attrs)%2 == 1 {
		return ctx
	}
	for i := 0; i < len(attrs); i += 2 {
		ctx = span.WithField(ctx, attrs[i], attrs[i+1])
	}
	return ctx
}

// GetLogs implements the logic for interLink pod logs retrieval.
func (p *VirtualKubeletProvider) GetLogs(ctx context.Context, namespace, podName, containerName string, opts api.ContainerLogOpts) (io.ReadCloser, error) {
	var span trace.Span
	ctx, span = trace.StartSpan(ctx, "GetLogs") //nolint: ineffassign,staticcheck
	defer span.End()

	// Add namespace and name as attributes to the current span.
	ctx = addAttributes(ctx, span, NamespaceKey, namespace, NameKey, podName)

	log.G(ctx).Infof("receive GetPodLogs %q", podName)

	key, err := buildKeyFromNames(namespace, podName)
	if err != nil {
		log.G(ctx).Error(err)
	}

	logsRequest := commonIL.LogStruct{
		Namespace:     namespace,
		PodUID:        string(p.pods[key].UID),
		PodName:       podName,
		ContainerName: containerName,
		Opts:          commonIL.ContainerLogOpts(opts),
	}

	return LogRetrieval(ctx, p.config, logsRequest)
}

// GetStatsSummary returns dummy stats for all pods known by this provider.
func (p *VirtualKubeletProvider) GetStatsSummary(ctx context.Context) (*stats.Summary, error) {
	var span trace.Span
	_, span = trace.StartSpan(ctx, "GetStatsSummary") //nolint: ineffassign,staticcheck
	defer span.End()

	// Grab the current timestamp so we can report it as the time the stats were generated.
	time := metav1.NewTime(time.Now())

	// Create the Summary object that will later be populated with node and pod stats.
	res := &stats.Summary{}

	// Populate the Summary object with basic node stats.
	res.Node = stats.NodeStats{
		NodeName:  p.nodeName,
		StartTime: metav1.NewTime(p.startTime),
	}

	// Populate the Summary object with dummy stats for each pod known by this provider.
	for _, pod := range p.pods {
		var (
			// totalUsageNanoCores will be populated with the sum of the values of UsageNanoCores computes across all containers in the pod.
			totalUsageNanoCores uint64
			// totalUsageBytes will be populated with the sum of the values of UsageBytes computed across all containers in the pod.
			totalUsageBytes uint64
		)

		// Create a PodStats object to populate with pod stats.
		pss := stats.PodStats{
			PodRef: stats.PodReference{
				Name:      pod.Name,
				Namespace: pod.Namespace,
				UID:       string(pod.UID),
			},
			StartTime: pod.CreationTimestamp,
		}

		// Iterate over all containers in the current pod to compute dummy stats.
		for _, container := range pod.Spec.Containers {
			// Grab a dummy value to be used as the total CPU usage.
			// The value should fit a uint32 in order to avoid overflows later on when computing pod stats.
			dummyUsageNanoCores := uint64(rand.Uint32())
			totalUsageNanoCores += dummyUsageNanoCores
			// Create a dummy value to be used as the total RAM usage.
			// The value should fit a uint32 in order to avoid overflows later on when computing pod stats.
			dummyUsageBytes := uint64(rand.Uint32())
			totalUsageBytes += dummyUsageBytes
			// Append a ContainerStats object containing the dummy stats to the PodStats object.
			pss.Containers = append(pss.Containers, stats.ContainerStats{
				Name:      container.Name,
				StartTime: pod.CreationTimestamp,
				CPU: &stats.CPUStats{
					Time:           time,
					UsageNanoCores: &dummyUsageNanoCores,
				},
				Memory: &stats.MemoryStats{
					Time:       time,
					UsageBytes: &dummyUsageBytes,
				},
			})
		}

		// Populate the CPU and RAM stats for the pod and append the PodsStats object to the Summary object to be returned.
		pss.CPU = &stats.CPUStats{
			Time:           time,
			UsageNanoCores: &totalUsageNanoCores,
		}
		pss.Memory = &stats.MemoryStats{
			Time:       time,
			UsageBytes: &totalUsageBytes,
		}
		res.Pods = append(res.Pods, pss)
	}

	// Return the dummy stats.
	return res, nil
}

// RetrievePodsFromCluster scans all pods registered to the K8S cluster and re-assigns the ones with a valid JobID to the Virtual Kubelet.
// This will run at the initiation time only
func (p *VirtualKubeletProvider) RetrievePodsFromCluster(ctx context.Context) error {
	ctx, span := trace.StartSpan(ctx, "RetrievePodsFromCluster")
	defer span.End()

	log.G(ctx).Info("Retrieving ALL Pods registered to the cluster and owned by VK")

	namespaces, err := p.clientSet.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		log.G(ctx).Error("Unable to retrieve all namespaces available in the cluster")
		return err
	}

	for _, ns := range namespaces.Items {
		podsList, err := p.clientSet.CoreV1().Pods(ns.Name).List(ctx, metav1.ListOptions{})
		if err != nil {
			log.G(ctx).Warning("Unable to retrieve pods from the namespace " + ns.Name)
		}
		for _, pod := range podsList.Items {
			if CheckIfAnnotationExists(&pod, "JobID") && p.nodeName == pod.Spec.NodeName {
				key, err := buildKeyFromNames(pod.Namespace, pod.Name)
				if err != nil {
					log.G(ctx).Error(err)
					return err
				}
				p.pods[key] = &pod
				p.notifier(&pod)
			}
		}

	}

	return err
}

// CheckIfAnnotationExists checks if a specific annotation (key) is available between the annotation of a pod
func CheckIfAnnotationExists(pod *v1.Pod, key string) bool {
	_, ok := pod.Annotations[key]

	if ok {
		return true
	} else {
		return false
	}
}

func (p *VirtualKubeletProvider) initClientSet(ctx context.Context) error {
	ctx, span := trace.StartSpan(ctx, "InitClientSet")
	defer span.End()

	if p.clientSet == nil {
		kubeconfig := os.Getenv("KUBECONFIG")

		config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			log.G(ctx).Error(err)
			return err
		}

		p.clientSet, err = kubernetes.NewForConfig(config)
		if err != nil {
			log.G(ctx).Error(err)
			return err
		}
	}

	return nil
}
