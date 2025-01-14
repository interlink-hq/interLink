package virtualkubelet

// Config holds the whole configuration
type Config struct {
	InterlinkURL      string      `yaml:"InterlinkURL"`
	InterlinkPort     string      `yaml:"InterlinkPort"`
	VKConfigPath      string      `yaml:"VKConfigPath"`
	VKTokenFile       string      `yaml:"VKTokenFile"`
	ServiceAccount    string      `yaml:"ServiceAccount"`
	Namespace         string      `yaml:"Namespace"`
	PodIP             string      `yaml:"PodIP"`
	VerboseLogging    bool        `yaml:"VerboseLogging"`
	ErrorsOnlyLogging bool        `yaml:"ErrorsOnlyLogging"`
	HTTP              HTTP        `yaml:"HTTP"`
	KubeletHTTP       HTTP        `yaml:"KubeletHTTP"`
	Resources         Resources   `yaml:"Resources"`
	NodeLabels        []string    `yaml:"NodeLabels"`
	NodeTaints        []TaintSpec `yaml:"NodeTaints"`
}

// HTTP contains configuration related to HTTP settings
type HTTP struct {
	Insecure bool `yaml:"Insecure"`
}

// Resources groups CPU, Memory, Pods, and GPU resources
type Resources struct {
	CPU       string `yaml:"CPU,omitempty"`
	Memory    string `yaml:"Memory,omitempty"`
	Pods      string `yaml:"Pods,omitempty"`
	NvidiaGPU string `yaml:"NvidiaGPU,omitempty"`
	AMDGPU    string `yaml:"AMDGPU,omitempty"`
	IntelGPU  string `yaml:"IntelGPU,omitempty"`
}

// TaintSpec represents a node taint with key, value, and effect
type TaintSpec struct {
	Key    string `yaml:"Key"`
	Value  string `yaml:"Value"`
	Effect string `yaml:"Effect"` // E.g., "NoSchedule"
}
