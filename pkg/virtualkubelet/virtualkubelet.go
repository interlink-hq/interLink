package virtualkubelet

import (
	"bytes"
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"fmt"
	"io"
	mathrand "math/rand"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"text/template"
	"time"

	"gopkg.in/yaml.v2"

	"github.com/containerd/containerd/log"
	"github.com/virtual-kubelet/virtual-kubelet/errdefs"
	"github.com/virtual-kubelet/virtual-kubelet/node/api"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	trace "go.opentelemetry.io/otel/trace"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	stats "k8s.io/kubelet/pkg/apis/stats/v1alpha1"

	types "github.com/interlink-hq/interlink/pkg/interlink"
	k8stypes "k8s.io/apimachinery/pkg/types"
)

//go:embed templates/wstunnel-template.yaml
var defaultWstunnelTemplate embed.FS

const (
	DefaultCPUCapacity     = "100"
	DefaultMemoryCapacity  = "3000G"
	DefaultPodCapacity     = "10000"
	DefaultGPUCapacity     = "0"
	DefaultFPGACapacity    = "0"
	DefaultListenPort      = 10250
	NamespaceKey           = "namespace"
	NameKey                = "name"
	CREATE                 = 0
	DELETE                 = 1
	nvidiaGPU              = "nvidia.com/gpu"
	amdGPU                 = "amd.com/gpu"
	intelGPU               = "intel.com/gpu"
	xilinxFPGA             = "xilinx.com/fpga"
	intelFPGA              = "intel.com/fpga"
	DefaultWstunnelCommand = "curl -L https://github.com/erebe/wstunnel/releases/download/v10.4.4/wstunnel_10.4.4_linux_amd64.tar.gz -o wstunnel.tar.gz && tar -xzvf wstunnel.tar.gz && chmod +x wstunnel\\n\\n./wstunnel client --http-upgrade-path-prefix %s %s ws://%s:80"
)

type WstunnelTemplateData struct {
	Name           string
	Namespace      string
	RandomPassword string
	ExposedPorts   []PortMapping
	WildcardDNS    string
}

type PortMapping struct {
	Port       int32
	TargetPort int32
	Name       string
	Protocol   string
}

// Increment the given IP address
func incrementIP(ip net.IP) {
	for j := len(ip) - 1; j >= 0; j-- {
		ip[j]++
		if ip[j] > 0 {
			break
		}
	}
}

func findFirstFreeIP(ipList, usedIPs []string, minIP, maxIP int) string {
	usedIPSet := make(map[string]bool)
	for _, ip := range usedIPs {
		usedIPSet[ip] = true
	}

	for _, ip := range ipList {
		if usedIPSet[ip] {
			continue
		}

		var numStr string
		if strings.Contains(ip, ".") {
			parts := strings.Split(ip, ".")
			numStr = parts[len(parts)-1]
		} else {
			numStr = ip
		}

		ipNum, err := strconv.Atoi(numStr)
		if err != nil {
			continue
		}

		if ipNum < minIP || ipNum > maxIP {
			continue
		}

		return ip
	}

	return ""
}

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

func PodPhase(_ Provider, phase string, podIP string) (v1.PodStatus, error) {
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
		HostIP:    podIP,
		PodIP:     podIP,
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

	// Set default WStunnel command if not provided
	if config.Network.WstunnelCommand == "" {
		config.Network.WstunnelCommand = DefaultWstunnelCommand
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
	podIPs               []string
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
			PodCIDR:    config.PodCIDR.Subnet,
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
		_, code, respBody, err := PingInterLink(ctx, p.config)
		if err != nil || code != 200 {
			// Use custom condition with InterLink status information
			errorMsg := fmt.Sprintf("Ping failed with code %d", code)
			if err != nil {
				errorMsg = fmt.Sprintf("Ping failed: %v", err)
			}
			if respBody != "" {
				errorMsg = fmt.Sprintf("%s. Response: %s", errorMsg, respBody)
			}
			p.node.Status.Conditions = NodeConditionWithInterlink(false, v1.ConditionFalse, "InterlinkPingFailed", errorMsg)

			// Also store in annotation for backwards compatibility
			if p.node.Annotations == nil {
				p.node.Annotations = make(map[string]string)
			}
			p.node.Annotations["interlink.virtual-kubelet.io/ping-response"] = ""
			p.onNodeChangeCallback(p.node)
			log.G(ctx).Error("Ping Failed with exit code: ", code)
			log.G(ctx).Error("Error: ", err)
		} else {
			// Use custom condition with InterLink status information
			successMsg := fmt.Sprintf("Ping successful with code %d", code)
			if respBody != "" {
				successMsg = fmt.Sprintf("%s. Response: %s", successMsg, respBody)
			}
			p.node.Status.Conditions = NodeConditionWithInterlink(true, v1.ConditionTrue, "InterlinkPingSuccessful", successMsg)

			// Also store in annotation for backwards compatibility
			if p.node.Annotations == nil {
				p.node.Annotations = make(map[string]string)
			}
			p.node.Annotations["interlink.virtual-kubelet.io/ping-response"] = respBody
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

// hasExposedPorts checks if any container in the pod has exposed ports
func hasExposedPorts(pod *v1.Pod) bool {
	for _, container := range pod.Spec.Containers {
		if len(container.Ports) > 0 {
			return true
		}
	}
	for _, container := range pod.Spec.InitContainers {
		if len(container.Ports) > 0 {
			return true
		}
	}
	return false
}

// generateRandomPassword creates a random password for the wstunnel
func generateRandomPassword() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		// Fallback to time-based random source if crypto/rand fails
		source := mathrand.NewSource(time.Now().UnixNano())
		rng := mathrand.New(source)
		for i := range bytes {
			bytes[i] = byte(rng.Intn(256))
		}
	}
	return hex.EncodeToString(bytes)
}

// createDummyPod creates wstunnel infrastructure from template for containers with exposed ports
func (p *Provider) createDummyPod(ctx context.Context, originalPod *v1.Pod) (*v1.Pod, *WstunnelTemplateData, error) {
	log.G(ctx).Infof("Creating wstunnel infrastructure for %s/%s with exposed ports", originalPod.Namespace, originalPod.Name)

	// If not exists, create the namespace for wstunnel
	if originalPod.Namespace == "" {
		return nil, nil, fmt.Errorf("pod namespace is empty")
	}
	namespace := originalPod.Namespace + "-wstunnel"
	_, err := p.clientSet.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, nil, fmt.Errorf("failed to get wstunnel namespace %s: %w", namespace, err)
		}
		// Create the namespace if it doesn't exist
		ns := &v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: namespace,
			},
		}
		_, err = p.clientSet.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create wstunnel namespace %s: %w", namespace, err)
		}
		log.G(ctx).Infof("Created wstunnel namespace %s", namespace)
	}

	// Generate template data
	templateData := WstunnelTemplateData{
		Name:           originalPod.Name + "-" + originalPod.Namespace,
		Namespace:      namespace,
		RandomPassword: generateRandomPassword(),
		ExposedPorts:   extractPortMappings(originalPod),
		WildcardDNS:    p.config.Network.WildcardDNS,
	}

	// Load and execute template
	manifestYAML, err := p.executeWstunnelTemplate(ctx, templateData)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to execute wstunnel template: %w", err)
	}

	// Apply the manifests
	createdPod, err := p.applyWstunnelManifests(ctx, manifestYAML)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to apply wstunnel manifests: %w", err)
	}

	log.G(ctx).Infof("Created wstunnel infrastructure for %s/%s", originalPod.Namespace, originalPod.Name)
	return createdPod, &templateData, nil
}

// executeWstunnelTemplate loads and executes the wstunnel template
func (p *Provider) executeWstunnelTemplate(ctx context.Context, data WstunnelTemplateData) (string, error) {
	var templateContent string

	// Try to load from custom path first
	if p.config.Network.WstunnelTemplatePath != "" {
		content, err := os.ReadFile(p.config.Network.WstunnelTemplatePath)
		if err != nil {
			log.G(ctx).Warningf("Failed to read custom template from %s: %v, using default", p.config.Network.WstunnelTemplatePath, err)
		} else {
			templateContent = string(content)
		}
	}

	// Fall back to embedded template
	if templateContent == "" {
		content, err := defaultWstunnelTemplate.ReadFile("templates/wstunnel-template.yaml")
		if err != nil {
			return "", fmt.Errorf("failed to read embedded template: %w", err)
		}
		templateContent = string(content)
	}

	// Parse and execute template
	tmpl, err := template.New("wstunnel").Parse(templateContent)
	if err != nil {
		return "", fmt.Errorf("failed to parse template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}

	return buf.String(), nil
}

// applyWstunnelManifests applies the generated manifests and returns the first created pod
func (p *Provider) applyWstunnelManifests(ctx context.Context, manifestYAML string) (*v1.Pod, error) {
	// Split the YAML into individual resources
	resources := strings.Split(manifestYAML, "---")

	decoder := serializer.NewCodecFactory(scheme.Scheme).UniversalDeserializer()
	var deploymentName string
	var namespace string

	for _, resource := range resources {
		resource = strings.TrimSpace(resource)
		if resource == "" {
			continue
		}

		// Decode the resource
		obj, _, err := decoder.Decode([]byte(resource), nil, nil)
		if err != nil {
			log.G(ctx).Warningf("Failed to decode resource: %v", err)
			continue
		}

		// Apply based on resource type
		switch o := obj.(type) {
		case *appsv1.Deployment:
			deployment, err := p.clientSet.AppsV1().Deployments(o.Namespace).Create(ctx, o, metav1.CreateOptions{})
			if err != nil {
				return nil, fmt.Errorf("failed to create deployment: %w", err)
			}
			deploymentName = deployment.Name
			namespace = deployment.Namespace
			log.G(ctx).Infof("Created deployment %s/%s", deployment.Namespace, deployment.Name)

		case *v1.Service:
			service, err := p.clientSet.CoreV1().Services(o.Namespace).Create(ctx, o, metav1.CreateOptions{})
			if err != nil {
				return nil, fmt.Errorf("failed to create service: %w", err)
			}
			log.G(ctx).Infof("Created service %s/%s", service.Namespace, service.Name)

		case *networkingv1.Ingress:
			ingress, err := p.clientSet.NetworkingV1().Ingresses(o.Namespace).Create(ctx, o, metav1.CreateOptions{})
			if err != nil {
				return nil, fmt.Errorf("failed to create ingress: %w", err)
			}
			log.G(ctx).Infof("Created ingress %s/%s", ingress.Namespace, ingress.Name)

		default:
			log.G(ctx).Warningf("Unsupported resource type: %T", obj)
		}
	}

	// Wait for deployment to create a pod and return it
	if deploymentName != "" {
		return p.waitForDeploymentPod(ctx, deploymentName, namespace)
	}

	return nil, fmt.Errorf("no deployment found in manifests")
}

// waitForDeploymentPod waits for a deployment to create a pod and returns the first one
func (p *Provider) waitForDeploymentPod(ctx context.Context, deploymentName, namespace string) (*v1.Pod, error) {
	timeout := 30 * time.Second
	start := time.Now()

	for time.Since(start) < timeout {
		pods, err := p.clientSet.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: fmt.Sprintf("app.kubernetes.io/component=%s", deploymentName),
		})
		if err != nil {
			log.G(ctx).Warningf("Failed to list pods for deployment %s: %v", deploymentName, err)
			time.Sleep(1 * time.Second)
			continue
		}

		if len(pods.Items) > 0 {
			return &pods.Items[0], nil
		}

		time.Sleep(1 * time.Second)
	}

	return nil, fmt.Errorf("no pod found for deployment %s within timeout", deploymentName)
}

// cleanupWstunnelResources removes all wstunnel resources for a given name and namespace
func (p *Provider) cleanupWstunnelResources(ctx context.Context, wstunnelName, namespace string) {
	log.G(ctx).Infof("Cleaning up wstunnel resources for %s/%s", namespace, wstunnelName)

	// Delete deployment
	err := p.clientSet.AppsV1().Deployments(namespace).Delete(ctx, wstunnelName, metav1.DeleteOptions{})
	if err != nil {
		log.G(ctx).Warningf("Failed to delete wstunnel deployment %s/%s: %v", namespace, wstunnelName, err)
	} else {
		log.G(ctx).Infof("Successfully deleted wstunnel deployment %s/%s", namespace, wstunnelName)
	}

	// Delete service
	err = p.clientSet.CoreV1().Services(namespace).Delete(ctx, wstunnelName, metav1.DeleteOptions{})
	if err != nil {
		log.G(ctx).Warningf("Failed to delete wstunnel service %s/%s: %v", namespace, wstunnelName, err)
	} else {
		log.G(ctx).Infof("Successfully deleted wstunnel service %s/%s", namespace, wstunnelName)
	}

	// Delete ingress
	err = p.clientSet.NetworkingV1().Ingresses(namespace).Delete(ctx, wstunnelName, metav1.DeleteOptions{})
	if err != nil {
		log.G(ctx).Warningf("Failed to delete wstunnel ingress %s/%s: %v", namespace, wstunnelName, err)
	} else {
		log.G(ctx).Infof("Successfully deleted wstunnel ingress %s/%s", namespace, wstunnelName)
	}
}

// extractPortMappings extracts port mappings for the template
func extractPortMappings(pod *v1.Pod) []PortMapping {
	var portMappings []PortMapping

	// Extract ports from container specs
	for _, container := range pod.Spec.Containers {
		for _, port := range container.Ports {
			portMappings = append(portMappings, PortMapping{
				Port:       port.ContainerPort,
				TargetPort: port.ContainerPort,
				Name:       port.Name,
				Protocol:   string(port.Protocol),
			})
		}
	}
	for _, container := range pod.Spec.InitContainers {
		for _, port := range container.Ports {
			portMappings = append(portMappings, PortMapping{
				Port:       port.ContainerPort,
				TargetPort: port.ContainerPort,
				Name:       port.Name,
				Protocol:   string(port.Protocol),
			})
		}
	}

	// Extract additional ports from annotation
	if extraPorts, exists := pod.Annotations["interlink.eu/wstunnel-extra-ports"]; exists {
		additionalPorts := parseExtraPortsAnnotation(extraPorts)
		portMappings = append(portMappings, additionalPorts...)
	}

	return portMappings
}

// parseExtraPortsAnnotation parses additional ports from comma-separated annotation
func parseExtraPortsAnnotation(annotation string) []PortMapping {
	var portMappings []PortMapping

	// Parse comma-separated format: "8080,9090:metrics:UDP,3000:api"
	ports := strings.Split(annotation, ",")
	for _, portStr := range ports {
		portStr = strings.TrimSpace(portStr)
		if portStr == "" {
			continue
		}

		// Parse format: "port:name:protocol" or "port:name" or "port"
		parts := strings.Split(portStr, ":")
		if len(parts) == 0 {
			continue
		}

		port, err := strconv.ParseInt(parts[0], 10, 32)
		if err != nil {
			continue
		}

		name := ""
		if len(parts) > 1 {
			name = parts[1]
		}

		protocol := "TCP"
		if len(parts) > 2 {
			protocol = strings.ToUpper(parts[2])
		}

		portMappings = append(portMappings, PortMapping{
			Port:       int32(port),
			TargetPort: int32(port),
			Name:       name,
			Protocol:   protocol,
		})
	}

	return portMappings
}

// hasExtraPortsAnnotation checks if pod has extra ports annotation
func hasExtraPortsAnnotation(pod *v1.Pod) bool {
	if pod.Annotations == nil {
		return false
	}
	extraPorts, exists := pod.Annotations["interlink.eu/wstunnel-extra-ports"]
	return exists && strings.TrimSpace(extraPorts) != ""
}

// shouldCreateWstunnel checks if wstunnel infrastructure should be created
func (p *Provider) shouldCreateWstunnel(pod *v1.Pod) bool {
	return p.config.Network.EnableTunnel && (hasExposedPorts(pod) || hasExtraPortsAnnotation(pod)) &&
		pod.Annotations["interlink.eu/pod-vpn"] == ""
}

// handleWstunnelCreation creates wstunnel infrastructure and returns the pod IP
func (p *Provider) handleWstunnelCreation(ctx context.Context, pod *v1.Pod) (string, error) {
	// Create wstunnel infrastructure outside virtual node for port exposure
	dummyPod, templateData, err := p.createDummyPod(ctx, pod)
	if err != nil {
		log.G(ctx).Errorf("Failed to create wstunnel infrastructure for %s/%s: %v", pod.Namespace, pod.Name, err)
		return "", fmt.Errorf("failed to create wstunnel infrastructure for exposed ports: %w", err)
	}

	// Wait for wstunnel pod to get an IP with timeout
	timeout := 30 * time.Second // Configurable timeout
	if timeoutStr := pod.Annotations["interlink.virtual-kubelet.io/wstunnel-timeout"]; timeoutStr != "" {
		if parsedTimeout, err := time.ParseDuration(timeoutStr); err == nil {
			timeout = parsedTimeout
		}
	}

	podIP, err := p.waitForWstunnelPodIP(ctx, dummyPod, timeout, pod.Name+"-wstunnel", pod.Namespace)
	if err != nil {
		return "", err
	}

	// Add wstunnel client command annotation to the original pod
	if err := p.addWstunnelClientAnnotation(ctx, pod, templateData); err != nil {
		log.G(ctx).Warningf("Failed to add wstunnel client annotation to pod %s/%s: %v", pod.Namespace, pod.Name, err)
	}

	return podIP, nil
}

// addWstunnelClientAnnotation adds the wstunnel client command annotation to the original pod
func (p *Provider) addWstunnelClientAnnotation(ctx context.Context, pod *v1.Pod, templateData *WstunnelTemplateData) error {
	if pod.Annotations == nil {
		pod.Annotations = make(map[string]string)
	}

	// Generate the ingress endpoint
	ingressEndpoint := fmt.Sprintf("%s-%s.%s", templateData.Name, templateData.Namespace, templateData.WildcardDNS)
	if templateData.WildcardDNS == "" {
		ingressEndpoint = templateData.Name
	}

	// Generate -R options for each exposed port
	var rOptions []string
	for _, port := range templateData.ExposedPorts {
		rOptions = append(rOptions, fmt.Sprintf("-R tcp://[::]:%d:localhost:%d", port.Port, port.Port))
	}

	// Get the wstunnel command template from config, or use default
	wstunnelCommandTemplate := p.config.Network.WstunnelCommand
	if wstunnelCommandTemplate == "" {
		wstunnelCommandTemplate = DefaultWstunnelCommand
	}

	// Create single command with all -R options
	command := fmt.Sprintf(wstunnelCommandTemplate,
		templateData.RandomPassword,
		strings.Join(rOptions, " "),
		ingressEndpoint,
	)

	// Add annotation with the complete command
	pod.Annotations["interlink.eu/wstunnel-client-commands"] = command

	log.G(ctx).Infof("Added wstunnel client command annotation to pod %s/%s", pod.Namespace, pod.Name)
	return nil
}

// waitForWstunnelPodIP waits for wstunnel pod to get an IP
func (p *Provider) waitForWstunnelPodIP(ctx context.Context, dummyPod *v1.Pod, timeout time.Duration, wstunnelName, namespace string) (string, error) {
	log.G(ctx).Infof("Waiting up to %v for wstunnel pod %s/%s to get an IP", timeout, dummyPod.Namespace, dummyPod.Name)

	start := time.Now()
	for time.Since(start) < timeout {
		updatedDummyPod, err := p.clientSet.CoreV1().Pods(dummyPod.Namespace).Get(ctx, dummyPod.Name, metav1.GetOptions{})
		if err != nil {
			log.G(ctx).Warningf("Failed to get wstunnel pod status: %v", err)
			time.Sleep(1 * time.Second)
			continue
		}

		if updatedDummyPod.Status.PodIP != "" {
			podIP := updatedDummyPod.Status.PodIP
			log.G(ctx).Infof("Using wstunnel pod IP %s for virtual pod %s/%s", podIP, namespace, dummyPod.Name)
			return podIP, nil
		}

		time.Sleep(1 * time.Second)
	}

	// Clean up the wstunnel infrastructure since it didn't get an IP
	p.cleanupWstunnelResources(ctx, wstunnelName, namespace)
	return "", fmt.Errorf("wstunnel pod %s/%s failed to get an IP within %v timeout", dummyPod.Namespace, dummyPod.Name, timeout)
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

	podIP := "127.0.0.1"

	// Handle wstunnel creation if needed
	if p.shouldCreateWstunnel(pod) {
		var err error
		podIP, err = p.handleWstunnelCreation(ctx, pod)
		if err != nil {
			return err
		}
	}

	if _, ok := pod.Annotations["interlink.eu/pod-vpn"]; ok {
		podsVPN, err := p.clientSet.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
		if err != nil {
			log.G(ctx).Warning("Get all pods attached to the VPN")
			return nil
		}

		log.G(ctx).Debug("Pod lists with pod-vpn enabled has len ", len(podsVPN.Items))

		for _, podVPN := range podsVPN.Items {
			if ip, ok := podVPN.Annotations["interlink.eu/pod-ip"]; ok {
				p.podIPs = append(p.podIPs, ip)
			}
		}

		// Get the CIDR of the virtual node
		podCIDR := p.node.Spec.PodCIDR
		if podCIDR == "" {
			return fmt.Errorf("node podCIDR not found")
		}

		_, subnet, err := net.ParseCIDR(podCIDR)
		if err != nil {
			return err
		}

		var ipList []string
		for ip := subnet.IP.Mask(subnet.Mask); subnet.Contains(ip); incrementIP(ip) {
			ipList = append(ipList, ip.String())
		}
		// Remove network address and broadcast address
		ipList = ipList[2 : len(ipList)-1]

		// get the minIP and maxIP from the config
		minIP := p.config.PodCIDR.MinIP
		maxIP := p.config.PodCIDR.MaxIP

		if minIP < 2 {
			log.G(ctx).Warn("MinIP is less than 2, setting it to 2")
			minIP = 2
		}

		if maxIP > 250 {
			log.G(ctx).Warn("MaxIP is greater than 250, setting it to 250")
			maxIP = 250
		}

		freeIP := findFirstFreeIP(ipList, p.podIPs, minIP, maxIP)
		if freeIP != "" {
			log.G(ctx).Info("First free IP: ", freeIP)
		} else {
			return fmt.Errorf("no free IP found")
		}

		p.podIPs = append(p.podIPs, freeIP)
		pod.Annotations["interlink.eu/pod-ip"] = freeIP
		podIP = freeIP
	} else if ip, ok := pod.Annotations["interlink.eu/pod-ip"]; ok {
		podIP = ip
	}

	// in case we have initContainers we need to stop main containers from executing for now ...
	if len(pod.Spec.InitContainers) > 0 {
		state = waitingState
		hasInitContainers = true

		// we put the phase in running but initialization phase to false
		status, err := PodPhase(*p, "Running", podIP)
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
		status, err := PodPhase(*p, "Pending", podIP)
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
				pod.Status, err = PodPhase(*p, "Pending", podIP)
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

	// Clean up wstunnel resources if tunnel is enabled and they exist and no VPN annotation
	if p.config.Network.EnableTunnel && hasExposedPorts(pod) {
		if _, hasVPN := pod.Annotations["interlink.eu/pod-vpn"]; !hasVPN {
			wstunnelName := pod.Name + "-" + pod.Namespace
			p.cleanupWstunnelResources(ctx, wstunnelName, pod.Namespace+"-wstunnel")
		}
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

		p.podIPs = []string{}

		podsVPN, err := p.clientSet.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
		if err != nil {
			log.G(ctx).Error(err)
		}

		log.G(ctx).Debug("Pod lists with pod-vpn enabled has len ", len(podsVPN.Items))

		for _, podVPN := range podsVPN.Items {
			if ip, ok := podVPN.Annotations["interlink.eu/pod-ip"]; ok {
				p.podIPs = append(p.podIPs, ip)
			}
		}

		token := ""
		if p.config.VKTokenFile != "" {
			b, err := os.ReadFile(p.config.VKTokenFile) // just pass the file name
			if err != nil {
				fmt.Print(err)
			}
			token = string(b)
		}

		for _, pod := range p.pods {
			if pod.Status.Phase != "Initializing" {
				if pod.Status.Phase == v1.PodFailed || pod.Status.Phase == v1.PodSucceeded {
					if p.pods[string(pod.UID)].Status.Phase != pod.Status.Phase {
						_, err := checkPodsStatus(ctx, p, pod, token, p.config)
						if err != nil {
							log.G(ctx).Error(err)
						}
						p.asyncUpdate(ctx, pod)
					}
				} else {
					_, err := checkPodsStatus(ctx, p, pod, token, p.config)
					if err != nil {
						log.G(ctx).Error(err)
					}
					p.asyncUpdate(ctx, pod)
				}
			}
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
	sessionNumber := mathrand.Intn(100000)
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
