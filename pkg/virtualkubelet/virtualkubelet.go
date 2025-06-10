package virtualkubelet

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v2"

	"github.com/containerd/containerd/log"
	"github.com/virtual-kubelet/virtual-kubelet/errdefs"
	"github.com/virtual-kubelet/virtual-kubelet/node/api"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	trace "go.opentelemetry.io/otel/trace"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	stats "k8s.io/kubelet/pkg/apis/stats/v1alpha1"

	types "github.com/interlink-hq/interlink/pkg/interlink"
	k8stypes "k8s.io/apimachinery/pkg/types"
)

const (
	DefaultCPUCapacity    = "100"
	DefaultMemoryCapacity = "3000G"
	DefaultPodCapacity    = "10000"
	DefaultGPUCapacity    = "0"
	DefaultFPGACapacity   = "0"
	DefaultListenPort     = 10250
	NamespaceKey          = "namespace"
	NameKey               = "name"
	CREATE                = 0
	DELETE                = 1
	nvidiaGPU             = "nvidia.com/gpu"
	amdGPU                = "amd.com/gpu"
	intelGPU              = "intel.com/gpu"
	xilinxFPGA            = "xilinx.com/fpga"
	intelFPGA             = "intel.com/fpga"
)

func TracerUpdate(ctx *context.Context, name string, pod *v1.Pod) {
	start := time.Now().Unix()
	tracer := otel.Tracer("interlink-service")

	var span trace.Span
	if pod != nil {
		*ctx, span = tracer.Start(*ctx, name, trace.WithAttributes(
			attribute.String("pod.name", pod.Name),
			attribute.String("pod.namespace", pod.Namespace),
			attribute.Int64("start.timestamp", start),
		))
		log.G(*ctx).Infof("receive %s %q", name, pod.Name)
	} else {
		*ctx, span = tracer.Start(*ctx, name, trace.WithAttributes(
			attribute.Int64("start.timestamp", start),
		))
	}
	defer span.End()
	defer types.SetDurationSpan(start, span)
}

func PodPhase(p Provider, phase string) (v1.PodStatus, error) {
	now := metav1.NewTime(time.Now())

	var podPhase v1.PodPhase
	var initialized v1.ConditionStatus
	var ready v1.ConditionStatus
	var scheduled v1.ConditionStatus

	switch phase {
	case "Running":
		podPhase = v1.PodRunning
		initialized = v1.ConditionTrue
		ready = v1.ConditionTrue
		scheduled = v1.ConditionTrue
	case "Pending":
		podPhase = v1.PodPending
		initialized = v1.ConditionTrue
		ready = v1.ConditionFalse
		scheduled = v1.ConditionTrue
	case "Failed":
		podPhase = v1.PodFailed
		initialized = v1.ConditionFalse
		ready = v1.ConditionFalse
		scheduled = v1.ConditionFalse
	default:
		return v1.PodStatus{}, fmt.Errorf("invalid pod phase specified: %s", phase)
	}

	return v1.PodStatus{
		Phase:     podPhase,
		HostIP:    p.internalIP,
		PodIP:     p.internalIP,
		StartTime: &now,
		Conditions: []v1.PodCondition{
			{
				Type:   v1.PodInitialized,
				Status: initialized,
			},
			{
				Type:   v1.PodReady,
				Status: ready,
			},
			{
				Type:   v1.PodScheduled,
				Status: scheduled,
			},
		},
	}, nil
}

func NodeCondition(ready bool) []v1.NodeCondition {
	var readyType v1.ConditionStatus
	var netType v1.ConditionStatus
	if ready {
		readyType = v1.ConditionTrue
		netType = v1.ConditionFalse
	} else {
		readyType = v1.ConditionFalse
		netType = v1.ConditionTrue
	}

	return []v1.NodeCondition{
		{
			Type:               "Ready",
			Status:             readyType,
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
			Status:             netType,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "RouteCreated",
			Message:            "RouteController created a route",
		},
	}
}

func NodeConditionWithInterlink(ready bool, interlinkStatus v1.ConditionStatus, interlinkReason, interlinkMessage string) []v1.NodeCondition {
	conditions := NodeCondition(ready)
	
	// Add custom InterLink connectivity condition
	interlinkCondition := v1.NodeCondition{
		Type:               "InterlinkConnectivity",
		Status:             interlinkStatus,
		LastHeartbeatTime:  metav1.Now(),
		LastTransitionTime: metav1.Now(),
		Reason:             interlinkReason,
		Message:            interlinkMessage,
	}
	
	return append(conditions, interlinkCondition)
}

func GetResources(config Config) v1.ResourceList {
	gpuCount := map[string]int{}
	fpgaCount := map[string]int{}

	for _, accelerator := range config.Resources.Accelerators {
		switch accelerator.ResourceType {
		case nvidiaGPU, amdGPU, intelGPU:
			gpuCount[accelerator.ResourceType] += accelerator.Available
		case xilinxFPGA, intelFPGA:
			fpgaCount[accelerator.ResourceType] += accelerator.Available
		}
	}

	resourceList := v1.ResourceList{
		"cpu":    resource.MustParse(config.Resources.CPU),
		"memory": resource.MustParse(config.Resources.Memory),
		"pods":   resource.MustParse(config.Resources.Pods),
	}

	for resourceType, count := range gpuCount {
		if count > 0 {
			resourceList[v1.ResourceName(resourceType)] = *resource.NewQuantity(int64(count), resource.DecimalSI)
		}
	}

	for resourceType, count := range fpgaCount {
		if count > 0 {
			resourceList[v1.ResourceName(resourceType)] = *resource.NewQuantity(int64(count), resource.DecimalSI)
		}
	}

	// log the resource list
	for key, value := range resourceList {
		log.G(context.Background()).Infof("Resource %s: %s", key, value.String())
	}

	return resourceList
}

func SetDefaultResource(config *Config) {
	if config.Resources.CPU == "" {
		config.Resources.CPU = DefaultCPUCapacity
	}
	if config.Resources.Memory == "" {
		config.Resources.Memory = DefaultMemoryCapacity
	}
	if config.Resources.Pods == "" {
		config.Resources.Pods = DefaultPodCapacity
	}

	for i, accelerator := range config.Resources.Accelerators {
		if accelerator.Available == 0 {
			switch accelerator.ResourceType {
			case nvidiaGPU, amdGPU, intelGPU:
				defaultGPUCapacity, err := strconv.Atoi(DefaultGPUCapacity)
				if err != nil {
					log.G(context.Background()).Errorf("Invalid default GPU capacity: %v", err)
					defaultGPUCapacity = 0
				}
				config.Resources.Accelerators[i].Available = defaultGPUCapacity
			case xilinxFPGA, intelFPGA:
				defaultFPGACapacity, err := strconv.Atoi(DefaultFPGACapacity)
				if err != nil {
					log.G(context.Background()).Errorf("Invalid default FPGA capacity: %v", err)
					defaultFPGACapacity = 0
				}
				config.Resources.Accelerators[i].Available = defaultFPGACapacity
			}
		}
	}
}

// Provider defines the properties of the virtual kubelet provider
type Provider struct {
	nodeName             string
	node                 *v1.Node
	operatingSystem      string
	internalIP           string
	daemonEndpointPort   int32
	pods                 map[string]*v1.Pod
	config               Config
	startTime            time.Time
	notifier             func(*v1.Pod)
	onNodeChangeCallback func(*v1.Node)
	clientSet            *kubernetes.Clientset
	clientHTTPTransport  *http.Transport
}

// NewProviderConfig takes user-defined configuration and fills the Virtual Kubelet provider struct
func NewProviderConfig(
	config Config,
	nodeName string,
	nodeVersion string,
	operatingSystem string,
	internalIP string,
	daemonEndpointPort int32,
	clientHTTPTransport *http.Transport,
) (*Provider, error) {
	SetDefaultResource(&config)

	lbls := map[string]string{
		"alpha.service-controller.kubernetes.io/exclude-balancer": "true",
		"kubernetes.io/os":       "virtual-kubelet",
		"kubernetes.io/hostname": nodeName,
		"kubernetes.io/role":     "agent",
		"node.kubernetes.io/exclude-from-external-load-balancers": "true",
		"virtual-node.interlink/type":                             "virtual-kubelet",
	}

	taints := []v1.Taint{
		{
			Key:    "virtual-node.interlink/no-schedule",
			Value:  strconv.FormatBool(true),
			Effect: v1.TaintEffectNoSchedule,
		},
	}

	// Add custom labels from config
	for _, label := range config.NodeLabels {

		parts := strings.SplitN(label, "=", 2)
		if len(parts) == 2 {
			lbls[parts[0]] = parts[1]
		} else {
			log.G(context.Background()).Warnf("Node label %q is not in the correct format. Should be key=value", label)
		}
	}

	for _, accelerator := range config.Resources.Accelerators {
		switch strings.ToLower(accelerator.ResourceType) {
		case "nvidia.com/gpu":
			lbls["nvidia-gpu-type"] = accelerator.Model
		case "xilinx.com/fpga":
			lbls["xilinx-fpga-type"] = accelerator.Model
		case "intel.com/fpga":
			lbls["intel-fpga-type"] = accelerator.Model
		default:
			log.G(context.Background()).Warnf("Unhandled accelerator resource type: %q", accelerator.ResourceType)
		}
	}

	for _, taint := range config.NodeTaints {
		log.G(context.Background()).Infof("Adding taint key=%q value=%q effect=%q", taint.Key, taint.Value, taint.Effect)

		var effect v1.TaintEffect

		switch taint.Effect {
		case "NoSchedule":
			effect = v1.TaintEffectNoSchedule
		case "PreferNoSchedule":
			effect = v1.TaintEffectPreferNoSchedule
		case "NoExecute":
			effect = v1.TaintEffectNoExecute
		default:
			effect = v1.TaintEffectNoSchedule
			log.G(context.Background()).Warnf("Unknown taint effect %q, defaulting to NoSchedule", taint.Effect)
		}

		taints = append(taints, v1.Taint{
			Key:    taint.Key,
			Value:  taint.Value,
			Effect: effect,
		})
	}

	node := v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   nodeName,
			Labels: lbls,
		},
		Spec: v1.NodeSpec{
			ProviderID: "external:///" + nodeName,
			Taints:     taints,
		},
		Status: v1.NodeStatus{
			NodeInfo: v1.NodeSystemInfo{
				KubeletVersion:  nodeVersion,
				Architecture:    "virtual-kubelet",
				OperatingSystem: "linux",
			},
			Addresses:       []v1.NodeAddress{{Type: v1.NodeInternalIP, Address: internalIP}},
			DaemonEndpoints: v1.NodeDaemonEndpoints{KubeletEndpoint: v1.DaemonEndpoint{Port: daemonEndpointPort}},
			Capacity:        GetResources(config),
			Allocatable:     GetResources(config),
			Conditions:      NodeCondition(false),
		},
	}

	provider := Provider{
		nodeName:            nodeName,
		node:                &node,
		operatingSystem:     operatingSystem,
		internalIP:          internalIP,
		daemonEndpointPort:  daemonEndpointPort,
		pods:                make(map[string]*v1.Pod),
		config:              config,
		startTime:           time.Now(),
		clientHTTPTransport: clientHTTPTransport,
	}

	return &provider, nil
}

// NewProvider creates a new Provider, which implements the PodNotifier and other virtual-kubelet interfaces
func NewProvider(
	ctx context.Context,
	providerConfig,
	nodeName,
	nodeVersion,
	operatingSystem string,
	internalIP string,
	daemonEndpointPort int32,
	clientHTTPTransport *http.Transport,
) (*Provider, error) {
	config, err := LoadConfig(ctx, providerConfig)
	if err != nil {
		return nil, err
	}
	log.G(ctx).Info("Init server with config:", config)
	return NewProviderConfig(
		config,
		nodeName,
		nodeVersion,
		operatingSystem,
		internalIP,
		daemonEndpointPort,
		clientHTTPTransport,
	)
}

// LoadConfig loads the given json configuration files and return a VirtualKubeletConfig struct
func LoadConfig(ctx context.Context, providerConfig string) (config Config, err error) {
	log.G(ctx).Info("Loading Virtual Kubelet config from " + providerConfig)
	data, err := os.ReadFile(providerConfig)
	if err != nil {
		return config, err
	}

	config = Config{}
	err = yaml.Unmarshal(data, &config)
	if err != nil {
		log.G(ctx).Fatal(err)
		return config, err
	}

	// config = configMap
	SetDefaultResource(&config)

	if _, err = resource.ParseQuantity(config.Resources.CPU); err != nil {
		return config, fmt.Errorf("invalid CPU value %v", config.Resources.CPU)
	}
	if _, err = resource.ParseQuantity(config.Resources.Memory); err != nil {
		return config, fmt.Errorf("invalid memory value %v", config.Resources.Memory)
	}
	if _, err = resource.ParseQuantity(config.Resources.Pods); err != nil {
		return config, fmt.Errorf("invalid pods value %v", config.Resources.Pods)
	}
	if _, err = resource.ParseQuantity(config.Resources.CPU); err != nil {
		return config, fmt.Errorf("invalid CPU value %v", config.Resources.CPU)
	}
	if _, err = resource.ParseQuantity(config.Resources.Memory); err != nil {
		return config, fmt.Errorf("invalid memory value %v", config.Resources.Memory)
	}
	if _, err = resource.ParseQuantity(config.Resources.Pods); err != nil {
		return config, fmt.Errorf("invalid pods value %v", config.Resources.Pods)
	}
	for _, accelerator := range config.Resources.Accelerators {
		quantity := resource.NewQuantity(int64(accelerator.Available), resource.DecimalSI)
		if _, err = resource.ParseQuantity(quantity.String()); err != nil {
			return config, fmt.Errorf("invalid value for accelerator %v (model: %v): %v", accelerator.ResourceType, accelerator.Model, err)
		}
	}

	return config, nil
}

// GetNode return the Node information at the initiation of a virtual node
func (p *Provider) GetNode() *v1.Node {
	return p.node
}

// NotifyNodeStatus runs once at initiation time and set the function to be used for node change notification (native of vk)
// it also starts a go routine for continously checking the node status and availability
func (p *Provider) NotifyNodeStatus(ctx context.Context, f func(*v1.Node)) {
	p.onNodeChangeCallback = f
	go p.nodeUpdate(ctx)
}

// nodeUpdate continously checks for node status and availability
func (p *Provider) nodeUpdate(ctx context.Context) {
	t := time.NewTimer(5 * time.Second)
	if !t.Stop() {
		<-t.C
	}

	log.G(ctx).Info("nodeLoop")

	if p.config.VKTokenFile != "" {
		_, err := os.ReadFile(p.config.VKTokenFile) // just pass the file name
		if err != nil {
			log.G(context.Background()).Fatal(err)
		}
	}

	for {
		t.Reset(30 * time.Second)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		_, code, responseBody, err := PingInterLink(ctx, p.config)
		if err != nil || code != 200 {
			var interlinkReason, interlinkMessage string
			if err != nil {
				interlinkReason = "InterlinkError"
				interlinkMessage = fmt.Sprintf("Error communicating with InterLink: %v", err)
			} else {
				interlinkReason = "InterlinkUnhealthy" 
				interlinkMessage = fmt.Sprintf("InterLink returned status code: %d", code)
			}
			if responseBody != "" {
				interlinkMessage += fmt.Sprintf(", Response: %s", responseBody)
			}
			
			p.node.Status.Conditions = NodeConditionWithInterlink(false, v1.ConditionFalse, interlinkReason, interlinkMessage)
			p.onNodeChangeCallback(p.node)
			log.G(ctx).Error("Ping Failed with exit code: ", code)
			log.G(ctx).Error("Error: ", err)
		} else {
			interlinkMessage := "InterLink is healthy and responding"
			if responseBody != "" {
				interlinkMessage += fmt.Sprintf(", Response: %s", responseBody)
			}
			
			p.node.Status.Conditions = NodeConditionWithInterlink(true, v1.ConditionTrue, "InterlinkHealthy", interlinkMessage)
			log.G(ctx).Info("Ping succeded with exit code: ", code)
			p.onNodeChangeCallback(p.node)
		}
		log.G(ctx).Info("endNodeLoop")
	}
}

// Ping the kubelet from the cluster, this will always be ok by design probably
func (p *Provider) Ping(_ context.Context) error {
	return nil
}

// CreatePod accepts a Pod definition and stores it in memory in p.pods
func (p *Provider) CreatePod(ctx context.Context, pod *v1.Pod) error {
	TracerUpdate(&ctx, "CreatePodVK", pod)

	hasInitContainers := false
	var state v1.ContainerState

	key := pod.UID

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
		status, err := PodPhase(*p, "Running")
		if err != nil {
			log.G(ctx).Error(err)
			return err
		}
		pod.Status = status
		err = p.UpdatePod(ctx, pod)
		if err != nil {
			log.G(ctx).Error(err)
		}
	} else {

		// if no init containers are there, go head and set phase to initialized
		status, err := PodPhase(*p, "Pending")
		if err != nil {
			log.G(ctx).Error(err)
			return err
		}

		pod.Status = status
		err = p.UpdatePod(ctx, pod)
		if err != nil {
			log.G(ctx).Error(err)
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
				// TODO if node in NotReady put it to Unknown/pending?
				log.G(ctx).Error(err)
				pod.Status, err = PodPhase(*p, "Pending")
				if err != nil {
					log.G(ctx).Error(err)
					return
				}

				err = p.UpdatePod(ctx, pod)
				if err != nil {
					log.G(ctx).Error(err)
				}

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

	p.pods[string(key)] = pod

	return nil
}

// UpdatePod accepts a Pod definition and updates its reference.
func (p *Provider) UpdatePod(ctx context.Context, pod *v1.Pod) error {
	TracerUpdate(&ctx, "UpdatePodVK", pod)

	p.notifier(pod)

	return nil
}

// DeletePod deletes the specified pod and drops it out of p.pods
func (p *Provider) DeletePod(ctx context.Context, pod *v1.Pod) (err error) {
	TracerUpdate(&ctx, "DeletePodVK", pod)

	log.G(ctx).Infof("receive DeletePod %q", pod.Name)

	key := pod.UID

	if _, exists := p.pods[string(key)]; !exists {
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
	err = p.UpdatePod(ctx, pod)
	if err != nil {
		return err
	}

	// delete from p.pods
	delete(p.pods, string(key))

	return nil
}

func (p *Provider) GetPod(_ context.Context, _ string, _ string) (*v1.Pod, error) {
	return &v1.Pod{}, fmt.Errorf("NOT IMPLEMENTED")
}

func (p *Provider) GetPodStatus(_ context.Context, _ string, _ string) (*v1.PodStatus, error) {
	return &v1.PodStatus{}, fmt.Errorf("NOT IMPLEMENTED")
}

// GetPodByUID returns a pod by name that is stored in memory.
func (p *Provider) GetPodByUID(ctx context.Context, namespace, name string, uid k8stypes.UID) (pod *v1.Pod, err error) {
	start := time.Now().Unix()
	tracer := otel.Tracer("interlink-service")
	ctx, span := tracer.Start(ctx, "GetPodVK", trace.WithAttributes(
		attribute.String("pod.name", name),
		attribute.String("pod.namespace", namespace),
		attribute.Int64("start.timestamp", start),
	))
	defer span.End()
	defer types.SetDurationSpan(start, span)

	log.G(ctx).Infof("receive GetPod %q", name)

	if pod, ok := p.pods[string(uid)]; ok {
		return pod, nil
	}

	return nil, errdefs.NotFoundf("pod \"%s/%s\" is not known to the provider", namespace, name)
}

// GetPodStatusByUID returns the status of a pod by name that is "running".
// returns nil if a pod by that name is not found.
func (p *Provider) GetPodStatusByUID(ctx context.Context, namespace, name string, uid k8stypes.UID) (*v1.PodStatus, error) {
	podTmp := v1.Pod{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
	TracerUpdate(&ctx, "GetPodStatusVK", &podTmp)

	pod, err := p.GetPodByUID(ctx, namespace, name, uid)
	if err != nil {
		return nil, err
	}

	return &pod.Status, nil
}

// GetPods returns a list of all pods known to be "running".
func (p *Provider) GetPods(ctx context.Context) ([]*v1.Pod, error) {
	TracerUpdate(&ctx, "GetPodsVK", nil)

	err := p.initClientSet(ctx)
	if err != nil {
		return nil, err
	}

	err = p.RetrievePodsFromCluster(ctx)
	if err != nil {
		return nil, err
	}

	var pods []*v1.Pod

	for _, pod := range p.pods {
		pods = append(pods, pod)
	}

	go p.statusLoop(ctx)
	return pods, nil
}

// NotifyPods is called to set a pod notifier callback function. Also starts the go routine to monitor all vk pods
func (p *Provider) NotifyPods(_ context.Context, f func(*v1.Pod)) {
	p.notifier = f
}

// statusLoop preiodically monitoring the status of all the pods in p.pods
func (p *Provider) statusLoop(ctx context.Context) {
	t := time.NewTimer(5 * time.Second)
	if !t.Stop() {
		<-t.C
	}

	for {
		log.G(ctx).Info("statusLoop")
		t.Reset(5 * time.Second)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}

		token := ""
		if p.config.VKTokenFile != "" {
			b, err := os.ReadFile(p.config.VKTokenFile) // just pass the file name
			if err != nil {
				fmt.Print(err)
			}
			token = string(b)
		}

		var podsList []*v1.Pod
		for _, pod := range p.pods {
			if pod.Status.Phase != "Initializing" {
				podsList = append(podsList, pod)
				// err := p.UpdatePod(ctx, pod)
				// if err != nil {
				// 	log.G(ctx).Error(err)
				// }
			}
		}

		if len(podsList) > 0 {
			_, err := checkPodsStatus(ctx, p, podsList, token, p.config)
			if err != nil {
				log.G(ctx).Error(err)
			}
			for _, pod := range p.pods {
				if pod.Status.Phase == v1.PodFailed || pod.Status.Phase == v1.PodSucceeded {
					if p.pods[string(pod.UID)].Status.Phase != pod.Status.Phase {
						p.pods[string(pod.UID)] = pod
						go p.asyncUpdate(ctx, pod)
					}
				} else {
					p.pods[string(pod.UID)] = pod
					go p.asyncUpdate(ctx, pod)
				}
			}
		} else {
			log.G(ctx).Info("No pods to monitor, waiting for the next loop to start")
		}

		log.G(ctx).Info("statusLoop=end")
	}
}

func (p *Provider) asyncUpdate(ctx context.Context, pod *v1.Pod) {
	err := p.UpdatePod(ctx, pod)
	if err != nil {
		log.G(ctx).Error(err)
	}
}

func AddSessionContext(req *http.Request, sessionContext string) {
	req.Header.Set("InterLink-Http-Session", sessionContext)
}

func GetSessionContextMessage(sessionContext string) string {
	return "HTTP InterLink session " + sessionContext + ": "
}

// GetLogs implements the logic for interLink pod logs retrieval.
func (p *Provider) GetLogs(ctx context.Context, namespace, podName, containerName string, opts api.ContainerLogOpts) (io.ReadCloser, error) {
	start := time.Now().Unix()
	tracer := otel.Tracer("interlink-service")
	ctx, span := tracer.Start(ctx, "GetLogsVK", trace.WithAttributes(
		attribute.Int64("start.timestamp", start),
	))
	defer span.End()
	defer types.SetDurationSpan(start, span)

	// For debugging purpose, when we have many API calls, we can differentiate each one.
	sessionNumber := rand.Intn(100000)
	sessionContext := "GetLogs#" + strconv.Itoa(sessionNumber)
	sessionContextMessage := GetSessionContextMessage(sessionContext)

	log.G(ctx).Infof(sessionContextMessage+"receive GetPodLogs %q", podName)

	pod, err := p.clientSet.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	logsRequest := types.LogStruct{
		Namespace:     namespace,
		PodUID:        string(pod.UID),
		PodName:       podName,
		ContainerName: containerName,
		Opts:          types.ContainerLogOpts(opts),
	}

	return LogRetrieval(ctx, p.config, logsRequest, p.clientHTTPTransport, sessionContext)
}

// GetStatsSummary returns dummy stats for all pods known by this provider.
func (p *Provider) GetStatsSummary(ctx context.Context) (*stats.Summary, error) {
	start := time.Now().Unix()
	tracer := otel.Tracer("interlink-service")
	_, span := tracer.Start(ctx, "GetStatsSummaryVK", trace.WithAttributes(
		attribute.Int64("start.timestamp", start),
	))
	defer span.End()
	defer types.SetDurationSpan(start, span)

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
			dummyUsageNanoCores := uint64(9999)
			totalUsageNanoCores += dummyUsageNanoCores
			// Create a dummy value to be used as the total RAM usage.
			// The value should fit a uint32 in order to avoid overflows later on when computing pod stats.
			dummyUsageBytes := uint64(9999)
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
func (p *Provider) RetrievePodsFromCluster(ctx context.Context) error {
	start := time.Now().Unix()
	tracer := otel.Tracer("interlink-service")
	ctx, span := tracer.Start(ctx, "RetrievePodsFromCluster", trace.WithAttributes(
		attribute.Int64("start.timestamp", start),
	))
	defer span.End()
	defer types.SetDurationSpan(start, span)

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
				p.pods[string(pod.UID)] = &pod
				p.notifier(&pod)
			}
		}

	}

	return err
}

// CheckIfAnnotationExists checks if a specific annotation (key) is available between the annotation of a pod
func CheckIfAnnotationExists(pod *v1.Pod, key string) bool {
	_, ok := pod.Annotations[key]

	return ok
}

func (p *Provider) initClientSet(ctx context.Context) error {
	start := time.Now().Unix()
	tracer := otel.Tracer("interlink-service")
	ctx, span := tracer.Start(ctx, "InitClientSet", trace.WithAttributes(
		attribute.Int64("start.timestamp", start),
	))
	defer span.End()
	defer types.SetDurationSpan(start, span)

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
