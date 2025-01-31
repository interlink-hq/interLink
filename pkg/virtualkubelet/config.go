package virtualkubelet

// Config holds the whole configuration
type Config struct {
	InterlinkURL       string `yaml:"InterlinkURL"`
	Interlinkport      string `yaml:"InterlinkPort"`
	KubernetesAPIAddr  string `yaml:"KubernetesApiAddr"`
	KubernetesAPIPort  string `yaml:"KubernetesApiPort"`
	KubernetesAPICaCrt string `yaml:"KubernetesApiCaCrt"`
	VKConfigPath       string `yaml:"VKConfigPath"`
	VKTokenFile        string `yaml:"VKTokenFile"`
	ServiceAccount     string `yaml:"ServiceAccount"`
	Namespace          string `yaml:"Namespace"`
	PodIP              string `yaml:"PodIP"`
	VerboseLogging     bool   `yaml:"VerboseLogging"`
	ErrorsOnlyLogging  bool   `yaml:"ErrorsOnlyLogging"`
	HTTP               HTTP   `yaml:"HTTP"`
	KubeletHTTP        HTTP   `yaml:"KubeletHTTP"`
	CPU                string `yaml:"CPU,omitempty"`
	Memory             string `yaml:"Memory,omitempty"`
	Pods               string `yaml:"Pods,omitempty"`
	GPU                string `yaml:"nvidia.com/gpu,omitempty"`
}

type HTTP struct {
	Insecure bool `yaml:"Insecure"`
}
