package interlink

import (
	"time"

	v1 "k8s.io/api/core/v1"
)

// PodCreateRequests is a struct holding data for a create request. Retrieved ConfigMaps and Secrets are held along the Pod description itself.
type PodCreateRequests struct {
	Pod        v1.Pod         `json:"pod"`
	ConfigMaps []v1.ConfigMap `json:"configmaps"`
	Secrets    []v1.Secret    `json:"secrets"`
}

// PodStatus is a simplified v1.Pod struct, holding only necessary variables to uniquely identify a job/service in the sidecar. It is used to request
type PodStatus struct {
	PodName      string               `json:"name"`
	PodUID       string               `json:"UID"`
	PodNamespace string               `json:"namespace"`
	JobID        string               `json:"JID"`
	Containers   []v1.ContainerStatus `json:"containers"`
}

// CreateStruct is the response to be received from interLink whenever asked to create a pod. It will allow for mapping remote ID with the pod UUID
type CreateStruct struct {
	PodUID string `json:"PodUID"`
	PodJID string `json:"PodJID"`
}

// RetrievedContainer is used in InterLink to rearrange data structure in a suitable way for the sidecar
type RetrievedContainer struct {
	Name       string         `json:"name"`
	ConfigMaps []v1.ConfigMap `json:"configMaps"`
	Secrets    []v1.Secret    `json:"secrets"`
	EmptyDirs  []string       `json:"emptyDirs"`
}

// RetrievedPoData is used in InterLink to rearrange data structure in a suitable way for the sidecar
type RetrievedPodData struct {
	Pod        v1.Pod               `json:"pod"`
	Containers []RetrievedContainer `json:"container"`
}

// ContainerLogOpts is a struct in which it is possible to specify options to retrieve logs from the sidecar
type ContainerLogOpts struct {
	Tail         int       `json:"Tail"`
	LimitBytes   int       `json:"Bytes"`
	Timestamps   bool      `json:"Timestamps"`
	Follow       bool      `json:"Follow"`
	Previous     bool      `json:"Previous"`
	SinceSeconds int       `json:"SinceSeconds"`
	SinceTime    time.Time `json:"SinceTime"`
}

// LogStruct is needed to identify the job/container running on the sidecar to retrieve the logs from. Using ContainerLogOpts struct allows to specify more options on how to collect logs
type LogStruct struct {
	Namespace     string           `json:"Namespace"`
	PodUID        string           `json:"PodUID"`
	PodName       string           `json:"PodName"`
	ContainerName string           `json:"ContainerName"`
	Opts          ContainerLogOpts `json:"Opts"`
}
