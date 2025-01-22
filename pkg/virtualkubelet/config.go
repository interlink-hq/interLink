package virtualkubelet

// Config holds the whole configuration
type Config struct {
	InterlinkURL      string      `yaml:"interlinkURL"`
	InterlinkPort     string      `yaml:"interlinkPort"`
	VKConfigPath      string      `yaml:"vkConfigPath"`
	VKTokenFile       string      `yaml:"vkTokenFile"`
	ServiceAccount    string      `yaml:"serviceAccount"`
	Namespace         string      `yaml:"namespace"`
	PodIP             string      `yaml:"podIP"`
	VerboseLogging    bool        `yaml:"verboseLogging"`
	ErrorsOnlyLogging bool        `yaml:"errorsOnlyLogging"`
	HTTP              HTTP        `yaml:"http"`
	KubeletHTTP       HTTP        `yaml:"kubeletHTTP"`
	Resources         Resources   `yaml:"resources"`
	NodeLabels        []string    `yaml:"nodeLabels"`
	NodeTaints        []TaintSpec `yaml:"nodeTaints"`
}

type HTTP struct {
	Insecure bool `yaml:"insecure"`
}

type Resources struct {
	CPU          string        `yaml:"cpu,omitempty"`
	Memory       string        `yaml:"memory,omitempty"`
	Pods         string        `yaml:"pods,omitempty"`
	Accelerators []Accelerator `yaml:"accelerators"`
}

type Accelerator struct {
	ResourceType string `yaml:"resource_type"`
	Model        string `yaml:"model"`
	Available    int    `yaml:"available"`
}

type TaintSpec struct {
	Key    string `yaml:"key"`
	Value  string `yaml:"value"`
	Effect string `yaml:"effect"`
}
