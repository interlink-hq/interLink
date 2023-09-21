package common

import (
	"io/fs"
	"time"

	v1 "k8s.io/api/core/v1"
)

const (
	RUNNING = 0
	STOP    = 1
	UNKNOWN = 2
)

type PodStatus struct {
	PodName      string `json:"name"`
	PodNamespace string `json:"namespace"`
	PodStatus    uint   `json:"status"`
}

type RetrievedContainer struct {
	Name       string         `json:"name"`
	ConfigMaps []v1.ConfigMap `json:"configMaps"`
	Secrets    []v1.Secret    `json:"secrets"`
	EmptyDirs  []string       `json:"emptyDirs"`
}

type RetrievedPodData struct {
	Pod        v1.Pod               `json:"pod"`
	Containers []RetrievedContainer `json:"container"`
}

type ConfigMapSecret struct {
	Key   string      `json:"Key"`
	Value string      `json:"Value"`
	Path  string      `json:"Path"`
	Kind  string      `json:"Kind"`
	Mode  fs.FileMode `json:"Mode"`
}

type InterLinkConfig struct {
	VKTokenFile    string `yaml:"VKTokenFile"`
	Interlinkurl   string `yaml:"InterlinkURL"`
	Sidecarurl     string `yaml:"SidecarURL"`
	Sbatchpath     string `yaml:"SbatchPath"`
	Scancelpath    string `yaml:"ScancelPath"`
	Interlinkport  string `yaml:"InterlinkPort"`
	Sidecarport    string `yaml:"SidecarPort"`
	Commandprefix  string `yaml:"CommandPrefix"`
	ExportPodData  bool   `yaml:"ExportPodData"`
	DataRootFolder string `yaml:"DataRootFolder"`
	ServiceAccount string `yaml:"ServiceAccount"`
	Namespace      string `yaml:"Namespace"`
	Tsocks         bool   `yaml:"Tsocks"`
	Tsockspath     string `yaml:"TsocksPath"`
	Tsocksconfig   string `yaml:"TsocksConfig"`
	Tsockslogin    string `yaml:"TsocksLoginNode"`
	BashPath       string `yaml:"BashPath"`
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

type ContainerLogOpts struct {
	Tail         int       `json:"Tail"`
	LimitBytes   int       `json:"Bytes"`
	Timestamps   bool      `json:"Timestamps"`
	Follow       bool      `json:"Follow"`
	Previous     bool      `json:"Previous"`
	SinceSeconds int       `json:"SinceSeconds"`
	SinceTime    time.Time `json:"SinceTime"`
}

type LogStruct struct {
	Namespace     string           `json:"Namespace"`
	PodName       string           `json:"PodName"`
	ContainerName string           `json:"ContainerName"`
	Opts          ContainerLogOpts `json:"Opts"`
}

type JidStruct struct {
	PodName string   `json:"PodName"`
	JIDs    []string `json:"JIDs"`
}
