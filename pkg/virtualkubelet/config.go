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
	CPU    string `yaml:"cpu,omitempty"`
	Memory string `yaml:"memory,omitempty"`
	Pods   string `yaml:"pods,omitempty"`
	GPU    GPU    `yaml:"gpu,omitempty"`
	FPGA   FPGA   `yaml:"fpga,omitempty"`
}

type GPU struct {
	Nvidia string `yaml:"nvidia,omitempty"`
	AMD    string `yaml:"amd,omitempty"`
	Intel  string `yaml:"intel,omitempty"`
}

type FPGA struct {
	Xilinx string `yaml:"xilinx,omitempty"`
	Intel  string `yaml:"intel,omitempty"`
}

type TaintSpec struct {
	Key    string `yaml:"key"`
	Value  string `yaml:"value"`
	Effect string `yaml:"effect"` // E.g., "NoSchedule"
}
