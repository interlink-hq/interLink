package common

import (
	"io/fs"

	v1 "k8s.io/api/core/v1"
)

const (
	RUNNING = 0
	STOP    = 1
	UNKNOWN = 2
)

type PodStatus struct {
	PodName   string `json:"podname"`
	PodStatus uint   `json:"podStatus"`
}

type StatusResponse struct {
	PodStatus []PodStatus `json:"podstatus"`
	ReturnVal string      `json:"returnVal"`
}

type Request struct {
	Pods map[string]*v1.Pod `json:"pods"`
}

type GenericRequestType struct {
	Body string `json:"body"`
}

type ConfigMapSecret struct {
	Key   string      `json:"Key"`
	Value string      `json:"Value"`
	Path  string      `json:"Path"`
	Kind  string      `json:"Kind"`
	Mode  fs.FileMode `json:"Mode"`
}

type RetrievedPodData struct {
	Pod           *v1.Pod
	ContainerName string `json:"ContainerName"`
	ConfigMaps    []*v1.ConfigMap
	Secrets       []*v1.Secret
	EmptyDirs     []string `json:"EmptyDirs"`
}

type InterLinkConfig struct {
	VKTokenFile    string `yaml:"VKTokenFile"`
	Interlinkurl   string `yaml:"InterlinkURL"`
	Sidecarurl     string `yaml:"SidecarURL"`
	Sbatchpath     string `yaml:"SbatchPath"`
	Scancelpath    string `yaml:"ScancelPath"`
	Interlinkport  string `yaml:"InterlinkPort"`
	Sidecarport    string
	Sidecarservice string `yaml:"SidecarService"`
	Commandprefix  string `yaml:"CommandPrefix"`
	ExportPodData  bool   `yaml:"ExportPodData"`
	DataRootFolder string `yaml:"DataRootFolder"`
	ServiceAccount string `yaml:"ServiceAccount"`
	Namespace      string `yaml:"Namespace"`
	Tsocks         bool   `yaml:"Tsocks"`
	Tsockspath     string `yaml:"TsocksPath"`
	Tsocksconfig   string `yaml:"TsocksConfig"`
	Tsockslogin    string `yaml:"TsocksLoginNode"`
	set            bool
}

type ServiceAccount struct {
	Name        string
	Token       string
	CA          string
	URL         string
	ClusterName string
	Config      string
}
