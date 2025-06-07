package virtualkubelet

// Config holds the whole configuration
type Config struct {
	InterlinkURL            string      `yaml:"InterlinkURL"`
	InterlinkPort           string      `yaml:"InterlinkPort"`
	KubernetesAPIAddr       string      `yaml:"KubernetesApiAddr"`
	KubernetesAPIPort       string      `yaml:"KubernetesApiPort"`
	KubernetesAPICaCrt      string      `yaml:"KubernetesApiCaCrt"`
	DisableProjectedVolumes bool        `yaml:"DisableProjectedVolumes"`
	JobScriptBuilderURL     string      `yaml:"JobScriptBuilderURL,omitempty"`
	VKConfigPath            string      `yaml:"VKConfigPath"`
	VKTokenFile             string      `yaml:"VKTokenFile"`
	ServiceAccount          string      `yaml:"ServiceAccount"`
	Namespace               string      `yaml:"Namespace"`
	PodIP                   string      `yaml:"PodIP"`
	PodCIDR                 PodCIDR     `yaml:"PodCIDR"`
	VerboseLogging          bool        `yaml:"VerboseLogging"`
	ErrorsOnlyLogging       bool        `yaml:"ErrorsOnlyLogging"`
	HTTP                    HTTP        `yaml:"HTTP"`
	KubeletHTTP             HTTP        `yaml:"KubeletHTTP"`
	Resources               Resources   `yaml:"Resources"`
	NodeLabels              []string    `yaml:"NodeLabels"`
	NodeTaints              []TaintSpec `yaml:"NodeTaints"`
	TLS                     TLSConfig   `yaml:"TLS,omitempty"`
}

// TLSConfig holds TLS/mTLS configuration for secure communication with interLink API
type TLSConfig struct {
	Enabled    bool   `yaml:"Enabled"`
	CertFile   string `yaml:"CertFile,omitempty"`
	KeyFile    string `yaml:"KeyFile,omitempty"`
	CACertFile string `yaml:"CACertFile,omitempty"`
}

type HTTP struct {
	Insecure bool   `yaml:"Insecure"`
	CaCert   string `yaml:"CaCert"`
}

type Resources struct {
	CPU          string        `yaml:"CPU,omitempty"`
	Memory       string        `yaml:"Memory,omitempty"`
	Pods         string        `yaml:"Pods,omitempty"`
	Accelerators []Accelerator `yaml:"Accelerators"`
}

type Accelerator struct {
	ResourceType string `yaml:"ResourceType"`
	Model        string `yaml:"Model"`
	Available    int    `yaml:"Available"`
}

type TaintSpec struct {
	Key    string `yaml:"Key"`
	Value  string `yaml:"Value"`
	Effect string `yaml:"Effect"`
}

type PodCIDR struct {
	Subnet string `yaml:"Subnet"`
	MaxIP  int    `yaml:"MaxIP"`
	MinIP  int    `yaml:"MinIP"`
}
