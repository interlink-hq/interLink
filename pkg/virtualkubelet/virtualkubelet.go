package virtualkubelet

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"os"
	"strconv"
	"time"

	"github.com/containerd/containerd/log"
	commonIL "github.com/intertwin-eu/interlink/pkg/common"
	"github.com/virtual-kubelet/virtual-kubelet/errdefs"
	"github.com/virtual-kubelet/virtual-kubelet/node/api"
	stats "github.com/virtual-kubelet/virtual-kubelet/node/api/statsv1alpha1"
	"github.com/virtual-kubelet/virtual-kubelet/trace"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	DefaultCPUCapacity    = "100"
	DefaultMemoryCapacity = "3000G"
	DefaultPodCapacity    = "10000"
	DefaultListenPort     = 10250
	NamespaceKey          = "namespace"
	NameKey               = "name"
	CREATE                = 0
	DELETE                = 1
)

func BuildKeyFromNames(namespace string, name string) (string, error) {
	return fmt.Sprintf("%s-%s", namespace, name), nil
}

func BuildKey(pod *v1.Pod) (string, error) {
	if pod.ObjectMeta.Namespace == "" {
		return "", fmt.Errorf("pod namespace not found")
	}

	if pod.ObjectMeta.Name == "" {
		return "", fmt.Errorf("pod name not found")
	}

	return BuildKeyFromNames(pod.ObjectMeta.Namespace, pod.ObjectMeta.Name)
}

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
}

type VirtualKubeletConfig struct {
	CPU    string `json:"cpu,omitempty"`
	Memory string `json:"memory,omitempty"`
	Pods   string `json:"pods,omitempty"`
}

// NewProviderConfig creates a new KNOCV0Provider. KNOC legacy provider does not implement the new asynchronous podnotifier interface
func NewProviderConfig(config VirtualKubeletConfig, nodeName, operatingSystem string, internalIP string, daemonEndpointPort int32) (*VirtualKubeletProvider, error) {

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
			// NodeInfo: v1.NodeSystemInfo{
			// 	KubeletVersion:  Version,
			// 	Architecture:    architecture,
			// 	OperatingSystem: linuxos,
			// },
			Addresses:       []v1.NodeAddress{{Type: v1.NodeInternalIP, Address: internalIP}},
			DaemonEndpoints: v1.NodeDaemonEndpoints{KubeletEndpoint: v1.DaemonEndpoint{Port: int32(daemonEndpointPort)}},
			Capacity: v1.ResourceList{
				"cpu":    resource.MustParse(config.CPU),
				"memory": resource.MustParse(config.Memory),
				"pods":   resource.MustParse(config.Pods),
			},
			Allocatable: v1.ResourceList{
				"cpu":    resource.MustParse(config.CPU),
				"memory": resource.MustParse(config.Memory),
				"pods":   resource.MustParse(config.Pods),
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
		onNodeChangeCallback: func(node *v1.Node) {
			return
		},
	}

	return &provider, nil
}

// NewProvider creates a new Provider, which implements the PodNotifier interface
func NewProvider(providerConfig, nodeName, operatingSystem string, internalIP string, daemonEndpointPort int32, ctx context.Context) (*VirtualKubeletProvider, error) {
	config, err := loadConfig(providerConfig, nodeName, ctx)
	if err != nil {
		return nil, err
	}
	return NewProviderConfig(config, nodeName, operatingSystem, internalIP, daemonEndpointPort)
}

// loadConfig loads the given json configuration files and yaml to communicate with InterLink.
func loadConfig(providerConfig, nodeName string, ctx context.Context) (config VirtualKubeletConfig, err error) {

	commonIL.NewInterLinkConfig()
	log.G(context.Background()).Info("Loading Virtual Kubelet config from " + providerConfig)
	data, err := os.ReadFile(providerConfig)
	if err != nil {
		return config, err
	}
	configMap := map[string]VirtualKubeletConfig{}
	err = json.Unmarshal(data, &configMap)
	if err != nil {
		return config, err
	}
	if _, exist := configMap[nodeName]; exist {
		config = configMap[nodeName]
		if config.CPU == "" {
			config.CPU = DefaultCPUCapacity
		}
		if config.Memory == "" {
			config.Memory = DefaultMemoryCapacity
		}
		if config.Pods == "" {
			config.Pods = DefaultPodCapacity
		}
	}

	if _, err = resource.ParseQuantity(config.CPU); err != nil {
		return config, fmt.Errorf("Invalid CPU value %v", config.CPU)
	}
	if _, err = resource.ParseQuantity(config.Memory); err != nil {
		return config, fmt.Errorf("Invalid memory value %v", config.Memory)
	}
	if _, err = resource.ParseQuantity(config.Pods); err != nil {
		return config, fmt.Errorf("Invalid pods value %v", config.Pods)
	}
	return config, nil
}

func (p *VirtualKubeletProvider) GetNode() *v1.Node {
	return p.node
}

func (p *VirtualKubeletProvider) NotifyNodeStatus(ctx context.Context, f func(*v1.Node)) {
	p.onNodeChangeCallback = f
}

func (p *VirtualKubeletProvider) Ping(ctx context.Context) error {
	// if p.pingDisabled {
	// 	return nil
	// }

	// start := time.Now()
	// klog.V(4).Infof("Checking whether the remote API server is ready")

	// // Get the foreigncluster using the given clusterID
	// fc, err := foreignclusterutils.GetForeignClusterByIDWithDynamicClient(ctx, p.dynClient, p.foreignClusterID)
	// if err != nil {
	// 	klog.Error(err)
	// 	return err
	// }

	// // Check the foreign API server status
	// if !foreignclusterutils.IsAPIServerReady(fc) {
	// 	return fmt.Errorf("[%s] API server readiness check failed", fc.Spec.ClusterIdentity.ClusterName)
	// }

	// klog.V(4).Infof("[%s] API server readiness check completed successfully in %v",
	// 	fc.Spec.ClusterIdentity.ClusterName, time.Since(start))
	// return nil
	return nil
}

// CreatePod accepts a Pod definition and stores it in memory.
func (p *VirtualKubeletProvider) CreatePod(ctx context.Context, pod *v1.Pod) error {
	ctx, span := trace.StartSpan(ctx, "CreatePod")
	var hasInitContainers bool = false
	var state v1.ContainerState
	defer span.End()
	distribution := "docker://"
	// Add the pod's coordinates to the current span.
	ctx = addAttributes(ctx, span, NamespaceKey, pod.Namespace, NameKey, pod.Name)
	key, err := BuildKey(pod)
	if err != nil {
		return err
	}
	now := metav1.NewTime(time.Now())
	running_state := v1.ContainerState{
		Running: &v1.ContainerStateRunning{
			StartedAt: now,
		},
	}
	waiting_state := v1.ContainerState{
		Waiting: &v1.ContainerStateWaiting{
			Reason: "Waiting for InitContainers",
		},
	}
	state = running_state

	// in case we have initContainers we need to stop main containers from executing for now ...
	if len(pod.Spec.InitContainers) > 0 {
		state = waiting_state
		hasInitContainers = true
		// run init container with remote execution enabled
		for _, container := range pod.Spec.InitContainers {
			// MUST TODO: Run init containers sequentialy and NOT all-together
			err = RemoteExecution(p, ctx, CREATE, distribution+container.Image, pod, container)
			if err != nil {
				return err
			}
		}

		pod.Status = v1.PodStatus{
			Phase:     v1.PodRunning,
			HostIP:    "127.0.0.1",
			PodIP:     "127.0.0.1",
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
		pod.Status = v1.PodStatus{
			Phase:     v1.PodRunning,
			HostIP:    "127.0.0.1",
			PodIP:     "127.0.0.1",
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
	// deploy main containers
	for _, container := range pod.Spec.Containers {
		var err error

		if !hasInitContainers {
			err = RemoteExecution(p, ctx, CREATE, distribution+container.Image, pod, container)
			if err != nil {
				return err
			}
		}
		pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, v1.ContainerStatus{
			Name:         container.Name,
			Image:        container.Image,
			Ready:        !hasInitContainers,
			RestartCount: 1,
			State:        state,
		})

	}

	p.pods[key] = pod
	p.notifier(pod)

	return nil
}

// UpdatePod accepts a Pod definition and updates its reference.
func (p *VirtualKubeletProvider) UpdatePod(ctx context.Context, pod *v1.Pod) error {
	ctx, span := trace.StartSpan(ctx, "UpdatePod")
	defer span.End()

	// Add the pod's coordinates to the current span.
	ctx = addAttributes(ctx, span, NamespaceKey, pod.Namespace, NameKey, pod.Name)

	log.G(ctx).Infof("receive UpdatePod %q", pod.Name)

	key, err := BuildKey(pod)
	if err != nil {
		return err
	}

	p.pods[key] = pod
	p.notifier(pod)

	return nil
}

// DeletePod deletes the specified pod out of memory.
func (p *VirtualKubeletProvider) DeletePod(ctx context.Context, pod *v1.Pod) (err error) {
	ctx, span := trace.StartSpan(ctx, "DeletePod")
	defer span.End()

	// Add the pod's coordinates to the current span.
	ctx = addAttributes(ctx, span, NamespaceKey, pod.Namespace, NameKey, pod.Name)

	log.G(ctx).Infof("receive DeletePod %q", pod.Name)

	key, err := BuildKey(pod)
	if err != nil {
		return err
	}

	if _, exists := p.pods[key]; !exists {
		return errdefs.NotFound("pod not found")
	}

	now := metav1.Now()
	pod.Status.Phase = v1.PodSucceeded
	pod.Status.Reason = "KNOCProviderPodDeleted"

	for _, container := range pod.Spec.Containers {
		err = RemoteExecution(p, ctx, DELETE, "", pod, container)
		if err != nil {
			log.G(ctx).Error(err)
			return err
		}

	}

	for _, container := range pod.Spec.InitContainers {
		err = RemoteExecution(p, ctx, DELETE, "", pod, container)
		if err != nil {
			log.G(ctx).Error(err)
			return err
		}

	}
	for idx := range pod.Status.ContainerStatuses {
		pod.Status.ContainerStatuses[idx].Ready = false
		pod.Status.ContainerStatuses[idx].State = v1.ContainerState{
			Terminated: &v1.ContainerStateTerminated{
				Message:    "KNOC provider terminated container upon deletion",
				FinishedAt: now,
				Reason:     "KNOCProviderPodContainerDeleted",
				// StartedAt:  pod.Status.ContainerStatuses[idx].State.Running.StartedAt,
			},
		}
	}
	for idx := range pod.Status.InitContainerStatuses {
		pod.Status.InitContainerStatuses[idx].Ready = false
		pod.Status.InitContainerStatuses[idx].State = v1.ContainerState{
			Terminated: &v1.ContainerStateTerminated{
				Message:    "KNOC provider terminated container upon deletion",
				FinishedAt: now,
				Reason:     "KNOCProviderPodContainerDeleted",
				// StartedAt:  pod.Status.InitContainerStatuses[idx].State.Running.StartedAt,
			},
		}
	}

	p.notifier(pod)
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

	key, err := BuildKeyFromNames(namespace, name)
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

	var pods []*v1.Pod

	for _, pod := range p.pods {
		pods = append(pods, pod)
	}

	return pods, nil
}

// NodeConditions returns a list of conditions (Ready, OutOfDisk, etc), for updates to the node status
// within Kubernetes.
func nodeConditions() []v1.NodeCondition {
	// TODO: Make this configurable
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

// NotifyPods is called to set a pod notifier callback function. This should be called before any operations are done
// within the provider.
func (p *VirtualKubeletProvider) NotifyPods(ctx context.Context, f func(*v1.Pod)) {
	p.notifier = f
	go p.statusLoop(ctx)
}

func (p *VirtualKubeletProvider) statusLoop(ctx context.Context) {
	t := time.NewTimer(5 * time.Second)
	if !t.Stop() {
		<-t.C
	}

	log.G(ctx).Info("statusLoop")

	b, err := os.ReadFile(commonIL.InterLinkConfigInst.VKTokenFile) // just pass the file name
	if err != nil {
		log.G(context.Background()).Fatal(err)
	}
	token := string(b)

	for {
		t.Reset(5 * time.Second)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}

		b, err = os.ReadFile(commonIL.InterLinkConfigInst.VKTokenFile) // just pass the file name
		if err != nil {
			fmt.Print(err)
		}
		token = string(b)
		err = checkPodsStatus(p, ctx, token)
		if err != nil {
			log.G(ctx).Error(err)
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

func (p *VirtualKubeletProvider) GetLogs(ctx context.Context, namespace, podName, containerName string, opts api.ContainerLogOpts) (io.ReadCloser, error) {
	return nil, fmt.Errorf("NOT IMPLEMENTED YET")
}

// GetStatsSummary returns dummy stats for all pods known by this provider.
func (p *VirtualKubeletProvider) GetStatsSummary(ctx context.Context) (*stats.Summary, error) {
	var span trace.Span
	ctx, span = trace.StartSpan(ctx, "GetStatsSummary") //nolint: ineffassign,staticcheck
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
