package common

import (
	v1 "k8s.io/api/core/v1"
)

const (
	RUNNING = 0
	STOP    = 1
	UNKNOWN = 2
)

type PodName struct {
	Name string `json:"podname"`
}

type PodStatus struct {
	PodStatus uint `json:"podStatus"`
}

type StatusResponse struct {
	PodName   []PodName   `json:"podname"`
	PodStatus []PodStatus `json:"podstatus"`
	ReturnVal string      `json:"returnVal"`
}

type Request struct {
	Pods map[string]*v1.Pod `json:"pods"`
}

type GenericRequestType struct {
	Body string `json:"body"`
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
