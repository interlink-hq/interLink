package virtualkubelet

// Config holds the whole configuration
type Config struct {
	InterlinkURL      string `yaml:"InterlinkURL"`
	Interlinkport     string `yaml:"InterlinkPort"`
	VKConfigPath      string `yaml:"VKConfigPath"`
	VKTokenFile       string `yaml:"VKTokenFile"`
	ServiceAccount    string `yaml:"ServiceAccount"`
	Namespace         string `yaml:"Namespace"`
	PodIP             string `yaml:"PodIP"`
	VerboseLogging    bool   `yaml:"VerboseLogging"`
	ErrorsOnlyLogging bool   `yaml:"ErrorsOnlyLogging"`
	CPU               string `yaml:"CPU,omitempty"`
	Memory            string `yaml:"Memory,omitempty"`
	Pods              string `yaml:"Pods,omitempty"`
	GPU               string `yaml:"nvidia.com/gpu,omitempty"`
}
