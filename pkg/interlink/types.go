// Package interlink provides types and utilities for the interLink project.
// interLink is an abstraction layer for executing Kubernetes pods on remote resources
// capable of managing container execution lifecycles.
package interlink

import (
	"time"

	v1 "k8s.io/api/core/v1"
)

// PodCreateRequests represents a request to create one or more pods on the remote system.
// It contains the pod specification along with all necessary supporting resources
// such as ConfigMaps, Secrets, and projected volumes that the pod requires.
type PodCreateRequests struct {
	// Pod is the Kubernetes pod specification to be created
	Pod v1.Pod `json:"pod"`
	// ConfigMaps contains all ConfigMaps referenced by the pod
	ConfigMaps []v1.ConfigMap `json:"configmaps"`
	// Secrets contains all Secrets referenced by the pod
	Secrets []v1.Secret `json:"secrets"`
	// ProjectedVolumeMaps contains projected volumes (e.g., ServiceAccount tokens, CA certificates)
	// created by ServiceAccounts (in Kubernetes >= 1.24). These are automatically added to pods
	// by the kubelet. The ConfigMap format holds file names as keys and content as values.
	ProjectedVolumeMaps []v1.ConfigMap `json:"projectedvolumesmaps"`
	// JobScriptBuilderURL is an optional endpoint URL that interLink should contact
	// to prepare a job script for offloading. When provided, the Virtual Kubelet
	// requests interLink to build a custom job script for the workload.
	JobScriptBuilderURL string `json:"jobscriptURL"`
}

// PodStatus represents the current status of a pod running on a remote system.
// It contains a simplified set of information needed to uniquely identify and
// track a job or service in the sidecar plugin. This struct is used for
// status queries and updates between the interLink API and plugins.
type PodStatus struct {
	// PodName is the name of the Kubernetes pod
	PodName string `json:"name"`
	// PodUID is the unique identifier of the Kubernetes pod
	PodUID string `json:"UID"`
	// PodNamespace is the namespace where the pod is deployed
	PodNamespace string `json:"namespace"`
	// JobID is the remote system's job identifier (e.g., SLURM job ID, container ID)
	JobID string `json:"JID"`
	// Containers holds the status of all regular containers in the pod
	Containers []v1.ContainerStatus `json:"containers"`
	// InitContainers holds the status of all init containers in the pod
	InitContainers []v1.ContainerStatus `json:"initContainers"`
}

// CreateStruct represents the response from the interLink API when a pod creation is requested.
// It provides the mapping between the Kubernetes pod UUID and the remote system's job identifier,
// enabling status tracking and management of the pod throughout its lifecycle.
type CreateStruct struct {
	// PodUID is the Kubernetes pod's unique identifier
	PodUID string `json:"PodUID"`
	// PodJID is the remote system's job identifier (e.g., SLURM job ID, Docker container ID)
	PodJID string `json:"PodJID"`
}

// RetrievedContainer represents a container with all its associated volume data.
// This structure is used by interLink to organize and package container-specific
// volume data (ConfigMaps, Secrets, etc.) in a format suitable for sidecar plugins.
type RetrievedContainer struct {
	// Name is the container's name as specified in the pod
	Name string `json:"name"`
	// ConfigMaps contains all ConfigMaps that this container needs to mount
	ConfigMaps []v1.ConfigMap `json:"configMaps"`
	// ProjectedVolumeMaps contains projected volumes (ServiceAccount tokens, CA certs, etc.)
	ProjectedVolumeMaps []v1.ConfigMap `json:"projectedvolumemaps"`
	// Secrets contains all Secrets that this container needs to mount
	Secrets []v1.Secret `json:"secrets"`
	// EmptyDirs contains paths to empty directories for this container.
	// EmptyDirs should be built on the plugin side rather than by interLink.
	// Currently, it holds paths like DATA_ROOT_DIR/emptydirs/volumeName, but this should be
	// a plugin implementation choice, similar to how ConfigMaps, ProjectedVolumeMaps, and Secrets are handled.
	EmptyDirs []string `json:"emptyDirs"`
}

// RetrievedPodData represents a complete pod with all its associated data.
// This structure packages a pod specification along with all volume data
// and optional job script configuration for transmission to sidecar plugins.
type RetrievedPodData struct {
	// Pod is the Kubernetes pod specification
	Pod v1.Pod `json:"pod"`
	// Containers contains volume data for each container in the pod
	Containers []RetrievedContainer `json:"container"`
	// JobScriptBuild contains configuration for building custom job scripts (optional)
	JobScriptBuild ScriptBuildConfig `json:"jobConfig,omitempty"`
	// JobScript contains the generated job script content (optional)
	JobScript string `json:"jobScript,omitempty"`
}

// ContainerLogOpts specifies options for retrieving container logs from sidecar plugins.
// These options control how logs are fetched, filtered, and formatted, matching
// Kubernetes' standard log retrieval parameters.
type ContainerLogOpts struct {
	// Tail specifies the number of lines from the end of the logs to show.
	// If 0, all available logs are returned.
	Tail int `json:"Tail"`
	// LimitBytes limits the number of bytes returned from the logs.
	// If 0, no byte limit is applied.
	LimitBytes int `json:"Bytes"`
	// Timestamps indicates whether to include timestamps in log output
	Timestamps bool `json:"Timestamps"`
	// Follow indicates whether to stream logs (follow mode)
	Follow bool `json:"Follow"`
	// Previous indicates whether to return logs from a previous container instance
	Previous bool `json:"Previous"`
	// SinceSeconds shows logs newer than this many seconds ago
	SinceSeconds int `json:"SinceSeconds"`
	// SinceTime shows logs after this timestamp
	SinceTime time.Time `json:"SinceTime"`
}

// LogStruct represents a log retrieval request for a specific container.
// It identifies the target container and includes options for how the logs
// should be retrieved and formatted.
type LogStruct struct {
	// Namespace is the Kubernetes namespace of the pod
	Namespace string `json:"Namespace"`
	// PodUID is the unique identifier of the pod
	PodUID string `json:"PodUID"`
	// PodName is the name of the pod
	PodName string `json:"PodName"`
	// ContainerName is the name of the container within the pod
	ContainerName string `json:"ContainerName"`
	// Opts specifies how the logs should be retrieved and formatted
	Opts ContainerLogOpts `json:"Opts"`
}

// SpanConfig holds configuration for OpenTelemetry spans.
// It's used to set additional attributes on tracing spans.
type SpanConfig struct {
	// HTTPReturnCode is the HTTP status code to record in the span
	HTTPReturnCode int
	// SetHTTPCode indicates whether to set the HTTP code attribute
	SetHTTPCode bool
}

// SpanOption is a functional option for configuring SpanConfig.
type SpanOption func(*SpanConfig)
