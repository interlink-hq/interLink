package virtualkubelet

// Config holds the whole configuration
type Config struct {
	InterlinkURL       string      `yaml:"InterlinkURL"`
	InterlinkPort      string      `yaml:"InterlinkPort"`
	KubernetesAPIAddr  string      `yaml:"KubernetesApiAddr"`
	KubernetesAPIPort  string      `yaml:"KubernetesApiPort"`
	KubernetesAPICaCrt string      `yaml:"KubernetesApiCaCrt"`
	VKConfigPath       string      `yaml:"VKConfigPath"`
	VKTokenFile        string      `yaml:"VKTokenFile"`
	ServiceAccount     string      `yaml:"ServiceAccount"`
	Namespace          string      `yaml:"Namespace"`
	PodIP              string      `yaml:"PodIP"`
	VerboseLogging     bool        `yaml:"VerboseLogging"`
	ErrorsOnlyLogging  bool        `yaml:"ErrorsOnlyLogging"`
	HTTP               HTTP        `yaml:"HTTP"`
	KubeletHTTP        HTTP        `yaml:"KubeletHTTP"`
	Resources          Resources   `yaml:"Resources"`
	NodeLabels         []string    `yaml:"NodeLabels"`
	NodeTaints         []TaintSpec `yaml:"NodeTaints"`
}

type HTTP struct {
	Insecure bool `yaml:"Insecure"`
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
