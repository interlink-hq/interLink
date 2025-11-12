package virtualkubelet

// Config holds the complete configuration for the Virtual Kubelet provider.
// It defines how the virtual node connects to the Kubernetes cluster and interLink API.
type Config struct {
	// InterlinkURL is the URL for connecting to the interLink API
	InterlinkURL string `yaml:"InterlinkURL"`
	// InterlinkPort specifies the port for the interLink API (for http/https)
	InterlinkPort string `yaml:"InterlinkPort"`
	// KubernetesAPIAddr is the Kubernetes API server address
	KubernetesAPIAddr string `yaml:"KubernetesApiAddr"`
	// KubernetesAPIPort specifies the Kubernetes API server port
	KubernetesAPIPort string `yaml:"KubernetesApiPort"`
	// KubernetesAPICaCrt is the CA certificate for Kubernetes API server verification
	KubernetesAPICaCrt string `yaml:"KubernetesApiCaCrt"`
	// DisableProjectedVolumes disables handling of Kubernetes projected volumes
	DisableProjectedVolumes bool `yaml:"DisableProjectedVolumes"`
	// JobScriptBuilderURL is an optional URL for an external job script builder
	JobScriptBuilderURL string `yaml:"JobScriptBuilderURL,omitempty"`
	// VKConfigPath is the path to the Virtual Kubelet configuration file
	VKConfigPath string `yaml:"VKConfigPath"`
	// VKTokenFile is the path to the token file for authenticating with the K8s API
	VKTokenFile string `yaml:"VKTokenFile"`
	// ServiceAccount is the name of the Kubernetes ServiceAccount to use
	ServiceAccount string `yaml:"ServiceAccount"`
	// Namespace specifies the Kubernetes namespace in which the Virtual Kubelet operates
	Namespace string `yaml:"Namespace"`
	// PodIP is the IP address assigned to the virtual node
	PodIP string `yaml:"PodIP"`
	// PodCIDR defines the CIDR range for pods assigned to the virtual node
	PodCIDR PodCIDR `yaml:"PodCIDR"`
	// VerboseLogging enables detailed logging output
	VerboseLogging bool `yaml:"VerboseLogging"`
	// ErrorsOnlyLogging restricts logging to error messages only
	ErrorsOnlyLogging bool `yaml:"ErrorsOnlyLogging"`
	// HTTP configures HTTP connection security
	HTTP HTTP `yaml:"HTTP"`
	// KubeletHTTP configures HTTP settings specific to Kubelet communication
	KubeletHTTP HTTP `yaml:"KubeletHTTP"`
	// Resources specifies compute resources available to the virtual node
	Resources Resources `yaml:"Resources"`
	// NodeLabels allows setting custom labels on the virtual node
	NodeLabels []string `yaml:"NodeLabels"`
	// NodeTaints allows setting taints on the virtual node
	NodeTaints []TaintSpec `yaml:"NodeTaints"`
	// TLS configures TLS/mTLS support for secure interLink API communication
	TLS TLSConfig `yaml:"TLS,omitempty"`
	// Network contains network-related settings for the virtual node
	Network Network `yaml:"Network,omitempty"`
	// SkipDownwardAPIResolution disables downward API resolution to enable scheduling pods with downward API
	SkipDownwardAPIResolution bool `yaml:"SkipDownwardAPIResolution,omitempty"`
}

// TLSConfig holds TLS/mTLS configuration for secure communication with interLink API.
type TLSConfig struct {
	// Enabled indicates whether TLS is enabled
	Enabled bool `yaml:"Enabled"`
	// CertFile is the path to the client certificate file for mTLS
	CertFile string `yaml:"CertFile,omitempty"`
	// KeyFile is the path to the client key file for mTLS
	KeyFile string `yaml:"KeyFile,omitempty"`
	// CACertFile is the path to the CA cert file for server verification
	CACertFile string `yaml:"CACertFile,omitempty"`
}

// HTTP defines security settings for HTTP connections.
// It determines whether connections are insecure and holds CA certificates.
type HTTP struct {
	// Insecure indicates whether to skip certificate verification (use with caution)
	Insecure bool `yaml:"Insecure"`
	// CaCert is the path to the CA certificate for verifying server connections
	CaCert string `yaml:"CaCert"`
}

// Resources defines the compute resources available to the virtual node.
// These values are reported to Kubernetes and used for pod scheduling decisions.
type Resources struct {
	// CPU specifies the total CPU capacity (e.g., "100", "2000m")
	CPU string `yaml:"CPU,omitempty"`
	// Memory specifies the total memory capacity (e.g., "128Gi", "64000Mi")
	Memory string `yaml:"Memory,omitempty"`
	// Pods specifies the maximum number of pods this node can handle
	Pods string `yaml:"Pods,omitempty"`
	// Accelerators lists hardware accelerators available on this node
	Accelerators []Accelerator `yaml:"Accelerators"`
}

// Accelerator represents a hardware accelerator (GPU, FPGA, etc.) available on the node.
type Accelerator struct {
	// ResourceType specifies the type of accelerator (e.g., "nvidia.com/gpu", "xilinx.com/fpga")
	ResourceType string `yaml:"ResourceType"`
	// Model specifies the specific model or variant of the accelerator
	Model string `yaml:"Model"`
	// Available indicates how many units of this accelerator are available
	Available int `yaml:"Available"`
}

// TaintSpec defines a Kubernetes taint to be applied to the virtual node.
// Taints prevent pods from being scheduled unless they have matching tolerations.
type TaintSpec struct {
	// Key is the taint key (e.g., "virtual-node.interlink/no-schedule")
	Key string `yaml:"Key"`
	// Value is the taint value
	Value string `yaml:"Value"`
	// Effect specifies the taint effect ("NoSchedule", "PreferNoSchedule", "NoExecute")
	Effect string `yaml:"Effect"`
}

// PodCIDR defines the CIDR range and IP allocation settings for pods on this node.
// This is used when pods need specific IP addresses within the node's network.
type PodCIDR struct {
	// Subnet specifies the CIDR subnet for pod IP allocation (e.g., "10.10.0.0/24")
	Subnet string `yaml:"Subnet"`
	// MaxIP specifies the maximum IP address number to allocate (e.g., 250)
	MaxIP int `yaml:"MaxIP"`
	// MinIP specifies the minimum IP address number to allocate (e.g., 2)
	MinIP int `yaml:"MinIP"`
}

// Network configures networking features for the virtual node.
// It includes settings for tunneling and service exposure.
type Network struct {
	// EnableTunnel enables WebSocket tunneling for pod port exposure
	EnableTunnel bool `yaml:"EnableTunnel" default:"false"`
	// WildcardDNS specifies the DNS domain for generating tunnel endpoints
	WildcardDNS string `yaml:"WildcardDNS,omitempty"`
	// WSTunnelExecutableURL specifies the URL to download the wstunnel executable (default is "https://github.com/erebe/wstunnel/releases/download/v10.4.4/wstunnel_10.4.4_linux_amd64.tar.gz")
	WSTunnelExecutableURL string `yaml:"WSTunnelExecutable,omitempty"`
	// WstunnelTemplatePath is the path to a custom wstunnel template file
	WstunnelTemplatePath string `yaml:"WstunnelTemplatePath,omitempty"`
	// WstunnelCommand specifies the command template for setting up wstunnel clients
	WstunnelCommand string `yaml:"WstunnelCommand,omitempty"`
	// FullMesh enables full mesh networking with slirp4netns and WireGuard
	FullMesh bool `yaml:"FullMesh" default:"false"`
	// MeshScriptTemplatePath is the path to a custom mesh.sh template file
	MeshScriptTemplatePath string `yaml:"MeshScriptTemplatePath,omitempty"`
	// ServiceCIDR specifies the CIDR range for Kubernetes services
	ServiceCIDR string `yaml:"ServiceCIDR,omitempty"`
	// PodCIDRCluster specifies the CIDR range for pods in the main cluster
	PodCIDRCluster string `yaml:"PodCIDRCluster,omitempty"`
	// DNSServiceIP specifies the IP address of the DNS service (e.g., kube-dns)
	DNSServiceIP string `yaml:"DNSServiceIP,omitempty"`
	// WireguardGoURL specifies the URL to download wireguard-go binary (default is "https://minio.131.154.98.45.myip.cloud.infn.it/public-data/wireguard-go")
	WireguardGoURL string `yaml:"WireguardGoURL,omitempty"`
	// WgToolURL specifies the URL to download wg tool binary (default is "https://git.zx2c4.com/wireguard-tools/snapshot/wireguard-tools-1.0.20210914.tar.xz")
	WgToolURL string `yaml:"WgToolURL,omitempty"`
	// Slirp4netnsURL specifies the URL to download slirp4netns binary (default is "https://github.com/rootless-containers/slirp4netns/releases/download/v1.2.3/slirp4netns-x86_64")
	Slirp4netnsURL string `yaml:"Slirp4netnsURL,omitempty"`
	// UnsharedMode is the flag for unshared network mode in slirp4netns
	UnshareMode string `yaml:"UnshareMode,omitempty"`
}
