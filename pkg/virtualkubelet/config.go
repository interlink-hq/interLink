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
	// KubeletCertFile is the path to the kubelet server certificate file (optional, for manual certificate management)
	KubeletCertFile string `yaml:"KubeletCertFile,omitempty"`
	// KubeletKeyFile is the path to the kubelet server key file (optional, for manual certificate management)
	KubeletKeyFile string `yaml:"KubeletKeyFile,omitempty"`
	// KubeletCSRSignerName specifies the signer name for CSR-based certificates (default: kubernetes.io/kubelet-serving)
	// Can be used with cert-manager: clusterissuers.cert-manager.io/<issuer-name>
	KubeletCSRSignerName string `yaml:"KubeletCSRSignerName,omitempty"`
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
	// DisableCSR disables CSR (CertificateSigningRequest) creation and uses self-signed certificates instead
	DisableCSR bool `yaml:"DisableCSR,omitempty"`
	// Pprof configures the pprof profiling server
	Pprof PprofConfig `yaml:"Pprof,omitempty"`
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

// PprofConfig holds configuration for the pprof profiling server.
type PprofConfig struct {
	// Enabled indicates whether the pprof server is enabled
	Enabled bool `yaml:"Enabled"`
	// Address is the listen address for pprof server (default: 127.0.0.1)
	Address string `yaml:"Address,omitempty"`
	// Port is the listen port for pprof server (default: 6060)
	Port string `yaml:"Port,omitempty"`
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
	// Available indicates how many units of this accelerator are available (as a Kubernetes quantity, e.g., "8", "16", "500m", "16Gi")
	Available string `yaml:"Available"`
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
	// WSTunnelExecutableURL specifies the URL to download the wstunnel executable (default is "https://github.com/interlink-hq/interlink-artifacts/raw/main/wstunnel/v10.4.4/linux-amd64/wstunnel")
	WSTunnelExecutableURL string `yaml:"WSTunnelExecutable,omitempty"`
	// WstunnelTemplatePath is the path to a custom wstunnel template file
	WstunnelTemplatePath string `yaml:"WstunnelTemplatePath,omitempty"`
	// WstunnelCommand specifies the command template for setting up wstunnel clients
	WstunnelCommand string `yaml:"WstunnelCommand,omitempty"`
	// IngressTLS enables TLS on generated wstunnel ingresses and makes the default client use wss://:443
	IngressTLS bool `yaml:"IngressTLS,omitempty" default:"false"`
	// IngressClusterIssuer is the cert-manager ClusterIssuer used when IngressTLS is enabled
	IngressClusterIssuer string `yaml:"IngressClusterIssuer,omitempty"`
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
	// WireguardGoURL specifies the URL to download wireguard-go binary (default is "https://github.com/interlink-hq/interlink-artifacts/raw/main/wireguard-go/v0.0.20201118/linux-amd64/wireguard-go")
	WireguardGoURL string `yaml:"WireguardGoURL,omitempty"`
	// WgToolURL specifies the URL to download wg tool binary (default is "https://github.com/interlink-hq/interlink-artifacts/raw/main/wgtools/v1.0.20210914/linux-amd64/wg")
	WgToolURL string `yaml:"WgToolURL,omitempty"`
	// Slirp4netnsURL specifies the URL to download slirp4netns binary (default is "https://github.com/interlink-hq/interlink-artifacts/raw/main/slirp4netns/v1.2.3/linux-amd64/slirp4netns")
	Slirp4netnsURL string `yaml:"Slirp4netnsURL,omitempty"`
	// UnsharedMode is the flag for unshared network mode in slirp4netns
	UnshareMode string `yaml:"UnshareMode,omitempty"`
	// ShadowMode selects which shadow implementation is rendered for offloaded pods
	// with exposed ports: "wstunnel" (default) or "ssh".
	ShadowMode string `yaml:"ShadowMode,omitempty"`
	// SSH configures the SSH port-forward shadow, used when ShadowMode is "ssh"
	SSH SSHTunnel `yaml:"SSH,omitempty"`
}

// Shadow implementations selectable through Network.ShadowMode.
const (
	// ShadowModeWstunnel exposes the offloaded pod's ports by having the workload
	// dial out to a public ingress and run a wstunnel client. This is the default.
	ShadowModeWstunnel = "wstunnel"
	// ShadowModeSSH exposes them the other way round: the shadow dials in to an SSH
	// login node and forwards each port to the compute node the job landed on. The
	// workload runs nothing, and no compute node needs outbound internet access.
	ShadowModeSSH = "ssh"
)

// Traffic-forwarding strategies selectable through SSHTunnel.ForwardMode.
const (
	// SSHForwardModePortForward uses `ssh -L`, and needs AllowTcpForwarding on the
	// login node. This is the default.
	SSHForwardModePortForward = "portforward"
	// SSHForwardModeExec pipes each connection through a command run on the login
	// node, for sites that do not grant TCP forwarding.
	SSHForwardModeExec = "exec"
)

// DefaultSSHExecConnectCommand relays a connection on stdin/stdout in "exec" mode.
const DefaultSSHExecConnectCommand = "nc"

// SSH authentication methods selectable through SSHTunnel.Auth.
const (
	// SSHAuthPublicKey authenticates with a private key from KeySecret.
	SSHAuthPublicKey = "publickey"
	// SSHAuthKerberos authenticates with GSSAPI, using a keytab from KeytabSecret.
	SSHAuthKerberos = "kerberos"
)

// SSHTunnel configures the SSH port-forward shadow.
//
// The shadow runs `ssh -N -L <port>:<compute node>:<port>` against the site's login
// node, one -L per exposed port, so cluster traffic reaches services inside an
// offloaded pod without the compute node needing any outbound connectivity. It
// covers the same ground as the wstunnel shadow, in the opposite direction; it does
// not give the offloaded pod access back into the cluster (see Network.FullMesh).
type SSHTunnel struct {
	// LoginHost is the SSH login node to forward through (required)
	LoginHost string `yaml:"LoginHost,omitempty"`
	// Port is the login node's SSH port (default 22)
	Port int `yaml:"Port,omitempty"`
	// User is the login name on the login node (required)
	User string `yaml:"User,omitempty"`
	// Image is the container image running in the shadow. It must provide an ssh
	// client, and kinit/klist when Auth is "kerberos".
	Image string `yaml:"Image,omitempty"`
	// Auth selects the authentication method: "publickey" (default) or "kerberos"
	Auth string `yaml:"Auth,omitempty"`
	// ForwardMode selects how traffic reaches the compute node:
	//
	//   "portforward" (default) — one `ssh -L` per exposed port. Cheapest and most
	//     direct, but the login node must set AllowTcpForwarding yes for this
	//     account. Sites that disable it refuse every channel with
	//     "administratively prohibited".
	//   "exec" — a local listener per exposed port, each connection piped through a
	//     command run on the login node (see ExecConnectCommand). Needs no
	//     forwarding privilege at all, at the cost of one ssh process per connection.
	ForwardMode string `yaml:"ForwardMode,omitempty"`
	// ExecConnectCommand is the command run on the login node in "exec" mode. It is
	// invoked as `<command> <compute node> <port>` and must relay the connection on
	// its stdin and stdout. Defaults to "nc".
	ExecConnectCommand string `yaml:"ExecConnectCommand,omitempty"`
	// KeySecret is the Secret holding the SSH private key ("publickey" auth)
	KeySecret string `yaml:"KeySecret,omitempty"`
	// KeySecretKey is the key inside KeySecret holding the private key (default "id_ed25519")
	KeySecretKey string `yaml:"KeySecretKey,omitempty"`
	// KeytabSecret is the Secret holding the Kerberos keytab ("kerberos" auth)
	KeytabSecret string `yaml:"KeytabSecret,omitempty"`
	// KeytabSecretKey is the key inside KeytabSecret holding the keytab (default "user.keytab")
	KeytabSecretKey string `yaml:"KeytabSecretKey,omitempty"`
	// Principal is the Kerberos principal to obtain a ticket for ("kerberos" auth)
	Principal string `yaml:"Principal,omitempty"`
	// Krb5ConfigMap is an optional ConfigMap holding a krb5.conf to mount at /etc/krb5.conf
	Krb5ConfigMap string `yaml:"Krb5ConfigMap,omitempty"`
	// KnownHostsConfigMap is an optional ConfigMap holding a known_hosts file. When
	// unset the shadow falls back to StrictHostKeyChecking=accept-new, which trusts
	// whatever key the login node presents on first contact.
	KnownHostsConfigMap string `yaml:"KnownHostsConfigMap,omitempty"`
	// ReplicateCredentials copies the referenced Secret and ConfigMaps from the
	// virtual kubelet's own namespace into the shadow's namespace, so offloaded pods
	// in arbitrary (e.g. per-user) namespaces work without pre-seeding credentials
	// everywhere. Defaults to true. Note this makes the credential readable by anyone
	// who can read Secrets in those namespaces.
	ReplicateCredentials *bool `yaml:"ReplicateCredentials,omitempty"`
	// NodeWaitTimeout bounds how long the shadow waits for the plugin to report the
	// compute node before failing (default "2h"). Queue waits are normal, so this is
	// generous by design.
	NodeWaitTimeout string `yaml:"NodeWaitTimeout,omitempty"`
	// ExtraOptions are additional ssh client options, each passed verbatim as -o <opt>
	ExtraOptions []string `yaml:"ExtraOptions,omitempty"`
}
