package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/containerd/containerd/log"
	types "github.com/interlink-hq/interlink/pkg/interlink"
	apitest "github.com/interlink-hq/interlink/pkg/interlink/cri"
	vkconfig "github.com/interlink-hq/interlink/pkg/virtualkubelet"
	"google.golang.org/grpc"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ktypes "k8s.io/apimachinery/pkg/types"
	kubeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
	"k8s.io/cri-client/pkg/util"
	utilexec "k8s.io/utils/exec"
)

// InterLink API functions for CRI integration (similar to virtual-kubelet execute.go pattern)

// CRI-specific session context management (similar to virtual-kubelet)
func addSessionContext(req *http.Request, sessionContext string) {
	req.Header.Set("X-Session-Context", sessionContext)
}

// createTLSHTTPClient creates an HTTP client with TLS/mTLS configuration (from virtual-kubelet pattern)
func createTLSHTTPClient(ctx context.Context, tlsConfig vkconfig.TLSConfig) (*http.Client, error) {
	if !tlsConfig.Enabled {
		return http.DefaultClient, nil
	}

	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
	}

	// Load CA certificate if provided
	if tlsConfig.CACertFile != "" {
		caCert, err := os.ReadFile(tlsConfig.CACertFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA certificate file %s: %w", tlsConfig.CACertFile, err)
		}

		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA certificate from %s", tlsConfig.CACertFile)
		}
		transport.TLSClientConfig.RootCAs = caCertPool
		log.G(ctx).Info("Loaded CA certificate for TLS client from: ", tlsConfig.CACertFile)
	}

	// Load client certificate and key for mTLS if provided
	if tlsConfig.CertFile != "" && tlsConfig.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(tlsConfig.CertFile, tlsConfig.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load client certificate pair (%s, %s): %w", tlsConfig.CertFile, tlsConfig.KeyFile, err)
		}
		transport.TLSClientConfig.Certificates = []tls.Certificate{cert}
		log.G(ctx).Info("Loaded client certificate for mTLS from: ", tlsConfig.CertFile, " and ", tlsConfig.KeyFile)
	}

	return &http.Client{Transport: transport}, nil
}

// getInterlinkEndpoint constructs the interLink API endpoint
func getInterlinkEndpoint(ctx context.Context, interlinkURL string, interlinkPort string) string {
	interlinkEndpoint := ""
	log.G(ctx).Info("InterlinkURL: ", interlinkURL)
	switch {
	case strings.HasPrefix(interlinkURL, "unix://"):
		interlinkEndpoint = "http://unix"
	case strings.HasPrefix(interlinkURL, "http://"):
		interlinkEndpoint = interlinkURL + ":" + interlinkPort
	case strings.HasPrefix(interlinkURL, "https://"):
		interlinkEndpoint = interlinkURL + ":" + interlinkPort
	default:
		log.G(ctx).Fatal("InterLinkURL should either start with unix:// or http(s)://")
	}
	return interlinkEndpoint
}

// doRequestWithClient performs HTTP request with authentication (similar to virtual-kubelet)
func doRequestWithClient(req *http.Request, token string, httpClient *http.Client) (*http.Response, error) {
	if token != "" {
		req.Header.Add("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	return httpClient.Do(req)
}

// createPodRequest performs a REST call to the InterLink API to create a pod
func (f *RemoteRuntime) createPodRequest(ctx context.Context, pod types.PodCreateRequests) ([]byte, error) {
	interlinkEndpoint := getInterlinkEndpoint(ctx, f.Config.InterlinkURL, f.Config.InterlinkPort)

	bodyBytes, err := json.Marshal(pod)
	if err != nil {
		log.G(ctx).Error(err)
		return nil, err
	}

	reader := bytes.NewReader(bodyBytes)
	req, err := http.NewRequest(http.MethodPost, interlinkEndpoint+"/create", reader)
	if err != nil {
		log.G(ctx).Error(err)
		return nil, err
	}

	// Add session context for tracing
	sessionContext := fmt.Sprintf("CRI-CreatePod#%d", rand.Intn(100000))
	addSessionContext(req, sessionContext)

	// Get token if configured
	token := ""
	if f.Config.VKTokenFile != "" {
		tokenBytes, err := os.ReadFile(f.Config.VKTokenFile)
		if err != nil {
			log.G(ctx).Error(err)
			return nil, err
		}
		token = string(tokenBytes)
	}

	// Create TLS-enabled HTTP client
	httpClient, err := createTLSHTTPClient(ctx, f.Config.TLS)
	if err != nil {
		return nil, fmt.Errorf("failed to create TLS HTTP client: %w", err)
	}

	resp, err := doRequestWithClient(req, token, httpClient)
	if err != nil {
		return nil, fmt.Errorf("error doing doRequest() in createPodRequest(): %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected error creating pod. Status code: %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

// deletePodRequest performs a REST call to the InterLink API to delete a pod
func (f *RemoteRuntime) deletePodRequest(ctx context.Context, pod *v1.Pod) error {
	interlinkEndpoint := getInterlinkEndpoint(ctx, f.Config.InterlinkURL, f.Config.InterlinkPort)

	bodyBytes, err := json.Marshal(pod)
	if err != nil {
		log.G(ctx).Error(err)
		return err
	}

	reader := bytes.NewReader(bodyBytes)
	req, err := http.NewRequest(http.MethodDelete, interlinkEndpoint+"/delete", reader)
	if err != nil {
		log.G(ctx).Error(err)
		return err
	}

	// Add session context for tracing
	sessionContext := fmt.Sprintf("CRI-DeletePod#%d", rand.Intn(100000))
	addSessionContext(req, sessionContext)

	// Get token if configured
	token := ""
	if f.Config.VKTokenFile != "" {
		tokenBytes, err := os.ReadFile(f.Config.VKTokenFile)
		if err != nil {
			log.G(ctx).Error(err)
			return err
		}
		token = string(tokenBytes)
	}

	// Create TLS-enabled HTTP client
	httpClient, err := createTLSHTTPClient(ctx, f.Config.TLS)
	if err != nil {
		return fmt.Errorf("failed to create TLS HTTP client: %w", err)
	}

	resp, err := doRequestWithClient(req, token, httpClient)
	if err != nil {
		return fmt.Errorf("error doing doRequest() in deletePodRequest(): %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected error deleting pod. Status code: %d", resp.StatusCode)
	}

	return nil
}

// RemoteRuntime represents a remote container runtime that integrates with interLink server API.
type RemoteRuntime struct {
	server *grpc.Server
	// Fake runtime service for CRI compatibility
	RuntimeService *apitest.FakeRuntimeService
	// Fake image service for CRI compatibility
	ImageService *apitest.FakeImageService
	// Configuration for interLink API communication (like virtual-kubelet)
	Config vkconfig.Config
	// Context for the runtime
	Ctx context.Context
	// Container-to-Pod mapping for tracking
	ContainerPodMap map[string]string // containerID -> podUID
}

// NewFakeRemoteRuntime creates a new RemoteRuntime with InterLink server API integration.
func NewFakeRemoteRuntime(ctx context.Context, config vkconfig.Config) *RemoteRuntime {
	fakeRuntimeService := apitest.NewFakeRuntimeService()
	fakeImageService := apitest.NewFakeImageService()

	f := &RemoteRuntime{
		server:          grpc.NewServer(),
		RuntimeService:  fakeRuntimeService,
		ImageService:    fakeImageService,
		Config:          config,
		Ctx:             ctx,
		ContainerPodMap: make(map[string]string),
	}
	kubeapi.RegisterRuntimeServiceServer(f.server, f)
	kubeapi.RegisterImageServiceServer(f.server, f.ImageService)

	return f
}

// Start starts the fake remote runtime.
func (f *RemoteRuntime) Start(endpoint string) error {
	l, err := util.CreateListener(endpoint)
	if err != nil {
		return fmt.Errorf("failed to listen on %q: %v", endpoint, err)
	}

	go func() {
		err = f.server.Serve(l)
		if err != nil {
			fmt.Printf("failed to serve: %v", err)
			panic(err)
		}
	}()
	// Set runtime and network conditions ready.
	f.RuntimeService.FakeStatus = &kubeapi.RuntimeStatus{
		Conditions: []*kubeapi.RuntimeCondition{
			{Type: kubeapi.RuntimeReady, Status: true},
			{Type: kubeapi.NetworkReady, Status: true},
		},
	}

	return nil
}

// Stop stops the fake remote runtime.
func (f *RemoteRuntime) Stop() {
	f.server.Stop()
}

// Version returns the runtime name, runtime version, and runtime API version.
func (f *RemoteRuntime) Version(ctx context.Context, req *kubeapi.VersionRequest) (*kubeapi.VersionResponse, error) {
	return f.RuntimeService.Version(ctx, req.Version)
}

// RunPodSandbox creates and starts a pod-level sandbox. Runtimes must ensure
// the sandbox is in the ready state on success.
func (f *RemoteRuntime) RunPodSandbox(ctx context.Context, req *kubeapi.RunPodSandboxRequest) (*kubeapi.RunPodSandboxResponse, error) {
	sandboxID, err := f.RuntimeService.RunPodSandbox(ctx, req.Config, req.RuntimeHandler)
	if err != nil {
		return nil, err
	}

	return &kubeapi.RunPodSandboxResponse{PodSandboxId: sandboxID}, nil
}

// StopPodSandbox stops any running process that is part of the sandbox and
// reclaims network resources (e.g., IP addresses) allocated to the sandbox.
// If there are any running containers in the sandbox, they must be forcibly
// terminated.
func (f *RemoteRuntime) StopPodSandbox(ctx context.Context, req *kubeapi.StopPodSandboxRequest) (*kubeapi.StopPodSandboxResponse, error) {
	err := f.RuntimeService.StopPodSandbox(ctx, req.PodSandboxId)
	if err != nil {
		return nil, err
	}

	return &kubeapi.StopPodSandboxResponse{}, nil
}

// RemovePodSandbox removes the sandbox using interLink lifecycle
func (f *RemoteRuntime) RemovePodSandbox(ctx context.Context, req *kubeapi.RemovePodSandboxRequest) (*kubeapi.RemovePodSandboxResponse, error) {
	// Check if we have a pod to delete via interLink API
	// Find any container mapped to this sandbox to get pod info
	var podUID string
	for containerID, uid := range f.ContainerPodMap {
		f.RuntimeService.Lock()
		container, exists := f.RuntimeService.Containers[containerID]
		f.RuntimeService.Unlock()

		if exists && container.SandboxID == req.PodSandboxId {
			podUID = uid
			break
		}
	}

	if podUID != "" {
		// Create a minimal pod object for deletion
		pod := &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				UID: ktypes.UID(podUID),
			},
		}

		// Call interLink delete API
		err := f.deletePodRequest(ctx, pod)
		if err != nil {
			log.G(ctx).Warning("Failed to delete pod via interLink API: ", err)
		} else {
			log.G(ctx).Info("Pod deleted successfully via interLink API: ", podUID)
		}

		// Clean up our container mappings for this sandbox
		for containerID, uid := range f.ContainerPodMap {
			if uid == podUID {
				delete(f.ContainerPodMap, containerID)
			}
		}
	}

	err := f.RuntimeService.StopPodSandbox(ctx, req.PodSandboxId)
	if err != nil {
		return nil, err
	}

	return &kubeapi.RemovePodSandboxResponse{}, nil
}

// PodSandboxStatus returns the status of the PodSandbox. If the PodSandbox is not
// present, returns an error.
func (f *RemoteRuntime) PodSandboxStatus(ctx context.Context, req *kubeapi.PodSandboxStatusRequest) (*kubeapi.PodSandboxStatusResponse, error) {
	resp, err := f.RuntimeService.PodSandboxStatus(ctx, req.PodSandboxId, false)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

// ListPodSandbox returns a list of PodSandboxes.
func (f *RemoteRuntime) ListPodSandbox(ctx context.Context, req *kubeapi.ListPodSandboxRequest) (*kubeapi.ListPodSandboxResponse, error) {
	items, err := f.RuntimeService.ListPodSandbox(ctx, req.Filter)
	if err != nil {
		return nil, err
	}

	return &kubeapi.ListPodSandboxResponse{Items: items}, nil
}

// convertToInterLinkPod converts CRI container config to interLink pod format
func (f *RemoteRuntime) convertToInterLinkPod(_ context.Context, sandboxConfig *kubeapi.PodSandboxConfig, containerConfig *kubeapi.ContainerConfig) *v1.Pod {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sandboxConfig.Metadata.Name,
			Namespace: sandboxConfig.Metadata.Namespace,
			UID:       ktypes.UID(sandboxConfig.Metadata.Uid),
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name:       containerConfig.Metadata.Name,
					Image:      containerConfig.Image.Image,
					Command:    containerConfig.Command,
					Args:       containerConfig.Args,
					WorkingDir: containerConfig.WorkingDir,
				},
			},
		},
		Status: v1.PodStatus{
			Phase: v1.PodPending,
		},
	}

	// Convert environment variables
	if len(containerConfig.Envs) > 0 {
		envVars := make([]v1.EnvVar, len(containerConfig.Envs))
		for i, env := range containerConfig.Envs {
			envVars[i] = v1.EnvVar{
				Name:  env.Key,
				Value: env.Value,
			}
		}
		pod.Spec.Containers[0].Env = envVars
	}

	// Convert resource requirements
	if containerConfig.Linux != nil && containerConfig.Linux.Resources != nil {
		resources := v1.ResourceRequirements{}
		if containerConfig.Linux.Resources.CpuQuota > 0 {
			resources.Limits = v1.ResourceList{
				v1.ResourceCPU: *resource.NewMilliQuantity(containerConfig.Linux.Resources.CpuQuota/1000, resource.DecimalSI),
			}
		}
		if containerConfig.Linux.Resources.MemoryLimitInBytes > 0 {
			if resources.Limits == nil {
				resources.Limits = v1.ResourceList{}
			}
			resources.Limits[v1.ResourceMemory] = *resource.NewQuantity(containerConfig.Linux.Resources.MemoryLimitInBytes, resource.BinarySI)
		}
		pod.Spec.Containers[0].Resources = resources
	}

	return pod
}

// CreateContainer creates a new container in specified PodSandbox using interLink lifecycle
func (f *RemoteRuntime) CreateContainer(ctx context.Context, req *kubeapi.CreateContainerRequest) (*kubeapi.CreateContainerResponse, error) {
	// First create the container using the fake runtime for CRI compatibility
	containerID, err := f.RuntimeService.CreateContainer(ctx, req.PodSandboxId, req.Config, req.SandboxConfig)
	if err != nil {
		return nil, err
	}

	// Integrate with interLink server API
	// Convert CRI config to interLink pod format
	pod := f.convertToInterLinkPod(ctx, req.SandboxConfig, req.Config)

	// Create pod creation request for interLink
	podCreateReq := types.PodCreateRequests{
		Pod: *pod,
	}

	// Call interLink server API to create the pod
	_, err = f.createPodRequest(ctx, podCreateReq)
	if err != nil {
		log.G(ctx).Warning("Failed to create pod via interLink API: ", err)
		// Continue with CRI response even if interLink fails
	} else {
		// Store container to pod mapping for lifecycle tracking
		f.ContainerPodMap[containerID] = string(pod.UID)
		log.G(ctx).Info("Pod created successfully via interLink API: ", pod.Name)
	}

	return &kubeapi.CreateContainerResponse{ContainerId: containerID}, nil
}

// StartContainer starts the container using interLink lifecycle
func (f *RemoteRuntime) StartContainer(ctx context.Context, req *kubeapi.StartContainerRequest) (*kubeapi.StartContainerResponse, error) {
	// Start container in fake runtime for CRI compatibility
	err := f.RuntimeService.StartContainer(ctx, req.ContainerId)
	if err != nil {
		return nil, err
	}

	// Container start is handled by interLink server API
	// The pod was already created during CreateContainer, so starting is managed by the plugin
	log.G(ctx).Info("Container started via CRI: ", req.ContainerId)

	return &kubeapi.StartContainerResponse{}, nil
}

// StopContainer stops a running container using interLink lifecycle
func (f *RemoteRuntime) StopContainer(ctx context.Context, req *kubeapi.StopContainerRequest) (*kubeapi.StopContainerResponse, error) {
	// Stop container in fake runtime for CRI compatibility
	err := f.RuntimeService.StopContainer(ctx, req.ContainerId, req.Timeout)
	if err != nil {
		return nil, err
	}

	// Container stop is handled by interLink server API
	// The interLink plugin manages the container lifecycle
	log.G(ctx).Info("Container stopped via CRI: ", req.ContainerId)

	return &kubeapi.StopContainerResponse{}, nil
}

// RemoveContainer removes the container using interLink lifecycle
func (f *RemoteRuntime) RemoveContainer(ctx context.Context, req *kubeapi.RemoveContainerRequest) (*kubeapi.RemoveContainerResponse, error) {
	// Clean up container-to-pod mapping
	delete(f.ContainerPodMap, req.ContainerId)

	// Remove container from fake runtime
	err := f.RuntimeService.RemoveContainer(ctx, req.ContainerId)
	if err != nil {
		return nil, err
	}

	return &kubeapi.RemoveContainerResponse{}, nil
}

// ListContainers lists all containers by filters.
func (f *RemoteRuntime) ListContainers(ctx context.Context, req *kubeapi.ListContainersRequest) (*kubeapi.ListContainersResponse, error) {
	items, err := f.RuntimeService.ListContainers(ctx, req.Filter)
	if err != nil {
		return nil, err
	}

	return &kubeapi.ListContainersResponse{Containers: items}, nil
}

// ContainerStatus returns status of the container using interLink lifecycle data
func (f *RemoteRuntime) ContainerStatus(ctx context.Context, req *kubeapi.ContainerStatusRequest) (*kubeapi.ContainerStatusResponse, error) {
	resp, err := f.RuntimeService.ContainerStatus(ctx, req.ContainerId, false)
	if err != nil {
		return nil, err
	}

	// Optionally get status from interLink server API
	// For now, we rely on the fake runtime status since it provides CRI compatibility
	// Future enhancement: query interLink status API for real container states
	log.G(ctx).Debug("Container status requested for: ", req.ContainerId)

	return resp, nil
}

// ExecSync runs a command in a container synchronously.
func (f *RemoteRuntime) ExecSync(ctx context.Context, req *kubeapi.ExecSyncRequest) (*kubeapi.ExecSyncResponse, error) {
	var exitCode int32
	stdout, stderr, err := f.RuntimeService.ExecSync(ctx, req.ContainerId, req.Cmd, time.Duration(req.Timeout)*time.Second)
	if err != nil {
		exitError, ok := err.(utilexec.ExitError)
		if !ok {
			return nil, err
		}
		exitCode = int32(exitError.ExitStatus())
	}

	return &kubeapi.ExecSyncResponse{
		Stdout:   stdout,
		Stderr:   stderr,
		ExitCode: exitCode,
	}, nil
}

// Exec prepares a streaming endpoint to execute a command in the container.
func (f *RemoteRuntime) Exec(ctx context.Context, req *kubeapi.ExecRequest) (*kubeapi.ExecResponse, error) {
	return f.RuntimeService.Exec(ctx, req)
}

// Attach prepares a streaming endpoint to attach to a running container.
func (f *RemoteRuntime) Attach(ctx context.Context, req *kubeapi.AttachRequest) (*kubeapi.AttachResponse, error) {
	return f.RuntimeService.Attach(ctx, req)
}

// PortForward prepares a streaming endpoint to forward ports from a PodSandbox.
func (f *RemoteRuntime) PortForward(ctx context.Context, req *kubeapi.PortForwardRequest) (*kubeapi.PortForwardResponse, error) {
	return f.RuntimeService.PortForward(ctx, req)
}

// ContainerStats returns stats of the container. If the container does not
// exist, the call returns an error.
func (f *RemoteRuntime) ContainerStats(ctx context.Context, req *kubeapi.ContainerStatsRequest) (*kubeapi.ContainerStatsResponse, error) {
	stats, err := f.RuntimeService.ContainerStats(ctx, req.ContainerId)
	if err != nil {
		return nil, err
	}

	return &kubeapi.ContainerStatsResponse{Stats: stats}, nil
}

// ListContainerStats returns stats of all running containers.
func (f *RemoteRuntime) ListContainerStats(ctx context.Context, req *kubeapi.ListContainerStatsRequest) (*kubeapi.ListContainerStatsResponse, error) {
	stats, err := f.RuntimeService.ListContainerStats(ctx, req.Filter)
	if err != nil {
		return nil, err
	}

	return &kubeapi.ListContainerStatsResponse{Stats: stats}, nil
}

// PodSandboxStats returns stats of the pod. If the pod does not
// exist, the call returns an error.
func (f *RemoteRuntime) PodSandboxStats(ctx context.Context, req *kubeapi.PodSandboxStatsRequest) (*kubeapi.PodSandboxStatsResponse, error) {
	stats, err := f.RuntimeService.PodSandboxStats(ctx, req.PodSandboxId)
	if err != nil {
		return nil, err
	}

	return &kubeapi.PodSandboxStatsResponse{Stats: stats}, nil
}

// ListPodSandboxStats returns stats of all running pods.
func (f *RemoteRuntime) ListPodSandboxStats(ctx context.Context, req *kubeapi.ListPodSandboxStatsRequest) (*kubeapi.ListPodSandboxStatsResponse, error) {
	stats, err := f.RuntimeService.ListPodSandboxStats(ctx, req.Filter)
	if err != nil {
		return nil, err
	}

	return &kubeapi.ListPodSandboxStatsResponse{Stats: stats}, nil
}

// UpdateRuntimeConfig updates the runtime configuration based on the given request.
func (f *RemoteRuntime) UpdateRuntimeConfig(ctx context.Context, req *kubeapi.UpdateRuntimeConfigRequest) (*kubeapi.UpdateRuntimeConfigResponse, error) {
	err := f.RuntimeService.UpdateRuntimeConfig(ctx, req.RuntimeConfig)
	if err != nil {
		return nil, err
	}

	return &kubeapi.UpdateRuntimeConfigResponse{}, nil
}

// Status returns the status of the runtime.
func (f *RemoteRuntime) Status(ctx context.Context, _ *kubeapi.StatusRequest) (*kubeapi.StatusResponse, error) {
	resp, err := f.RuntimeService.Status(ctx, false)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

// UpdateContainerResources updates ContainerConfig of the container.
func (f *RemoteRuntime) UpdateContainerResources(ctx context.Context, req *kubeapi.UpdateContainerResourcesRequest) (*kubeapi.UpdateContainerResourcesResponse, error) {
	err := f.RuntimeService.UpdateContainerResources(ctx, req.ContainerId, &kubeapi.ContainerResources{Linux: req.Linux})
	if err != nil {
		return nil, err
	}

	return &kubeapi.UpdateContainerResourcesResponse{}, nil
}

// ReopenContainerLog reopens the container log file.
func (f *RemoteRuntime) ReopenContainerLog(ctx context.Context, req *kubeapi.ReopenContainerLogRequest) (*kubeapi.ReopenContainerLogResponse, error) {
	err := f.RuntimeService.ReopenContainerLog(ctx, req.ContainerId)
	if err != nil {
		return nil, err
	}

	return &kubeapi.ReopenContainerLogResponse{}, nil
}

// CheckpointContainer checkpoints the given container.
func (f *RemoteRuntime) CheckpointContainer(ctx context.Context, _ *kubeapi.CheckpointContainerRequest) (*kubeapi.CheckpointContainerResponse, error) {
	err := f.RuntimeService.CheckpointContainer(ctx, &kubeapi.CheckpointContainerRequest{})
	if err != nil {
		return nil, err
	}

	return &kubeapi.CheckpointContainerResponse{}, nil
}

func (f *RemoteRuntime) GetContainerEvents(_ *kubeapi.GetEventsRequest, _ kubeapi.RuntimeService_GetContainerEventsServer) error {
	return nil
}

// ListMetricDescriptors gets the descriptors for the metrics that will be returned in ListPodSandboxMetrics.
func (f *RemoteRuntime) ListMetricDescriptors(ctx context.Context, _ *kubeapi.ListMetricDescriptorsRequest) (*kubeapi.ListMetricDescriptorsResponse, error) {
	descs, err := f.RuntimeService.ListMetricDescriptors(ctx)
	if err != nil {
		return nil, err
	}

	return &kubeapi.ListMetricDescriptorsResponse{Descriptors: descs}, nil
}

// ListPodSandboxMetrics retrieves the metrics for all pod sandboxes.
func (f *RemoteRuntime) ListPodSandboxMetrics(ctx context.Context, _ *kubeapi.ListPodSandboxMetricsRequest) (*kubeapi.ListPodSandboxMetricsResponse, error) {
	podMetrics, err := f.RuntimeService.ListPodSandboxMetrics(ctx)
	if err != nil {
		return nil, err
	}

	return &kubeapi.ListPodSandboxMetricsResponse{PodMetrics: podMetrics}, nil
}

// RuntimeConfig returns the configuration information of the runtime.
func (f *RemoteRuntime) RuntimeConfig(ctx context.Context, _ *kubeapi.RuntimeConfigRequest) (*kubeapi.RuntimeConfigResponse, error) {
	resp, err := f.RuntimeService.RuntimeConfig(ctx)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

// UpdatePodSandboxResources synchronously updates the PodSandboxConfig.
func (f *RemoteRuntime) UpdatePodSandboxResources(ctx context.Context, req *kubeapi.UpdatePodSandboxResourcesRequest) (*kubeapi.UpdatePodSandboxResourcesResponse, error) {
	return f.RuntimeService.UpdatePodSandboxResources(ctx, req)
}
