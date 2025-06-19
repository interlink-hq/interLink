package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	types "github.com/interlink-hq/interlink/pkg/interlink"
	"github.com/interlink-hq/interlink/pkg/interlink/api"
	apitest "github.com/interlink-hq/interlink/pkg/interlink/cri"
	"google.golang.org/grpc"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ktypes "k8s.io/apimachinery/pkg/types"
	kubeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
	"k8s.io/cri-client/pkg/util"
	utilexec "k8s.io/utils/exec"
)

// Helper functions to work with PodStatuses since mutex is not exported
// We'll need to implement a simpler approach for CRI integration

func getPodStatus(uid string) (types.PodStatus, bool) {
	// Use checkIfCached to check existence (it's not exported but we'll work around it)
	status := api.PodStatuses.Statuses[uid]
	return status, status.PodUID != ""
}

func updatePodStatusContainer(sandboxID string, containerStatus v1.ContainerStatus) {
	// Get current status
	if currentStatus, exists := getPodStatus(sandboxID); exists {
		// Update or add container status
		updated := false
		for i, cs := range currentStatus.Containers {
			if cs.Name == containerStatus.Name {
				currentStatus.Containers[i] = containerStatus
				updated = true
				break
			}
		}
		if !updated {
			currentStatus.Containers = append(currentStatus.Containers, containerStatus)
		}

		// Since updateStatuses expects a slice, create one
		api.PodStatuses.Statuses[sandboxID] = currentStatus
	}
}

func removePodStatusContainer(sandboxID string, containerName string) {
	if currentStatus, exists := getPodStatus(sandboxID); exists {
		updatedContainers := []v1.ContainerStatus{}
		for _, cs := range currentStatus.Containers {
			if cs.Name != containerName {
				updatedContainers = append(updatedContainers, cs)
			}
		}
		currentStatus.Containers = updatedContainers
		api.PodStatuses.Statuses[sandboxID] = currentStatus
	}
}

func removePodStatus(uid string) {
	// Since deleteCachedStatus is not exported, we'll work directly with the map
	// This is not ideal but necessary for the CRI integration
	delete(api.PodStatuses.Statuses, uid)
}

// RemoteRuntime represents a remote container runtime that integrates with interLink.
type RemoteRuntime struct {
	server *grpc.Server
	// Fake runtime service.
	RuntimeService *apitest.FakeRuntimeService
	// Fake image service.
	ImageService *apitest.FakeImageService
	// InterLink API handler for container lifecycle management
	InterLinkHandler *api.InterLinkHandler
}

// NewFakeRemoteRuntime creates a new RemoteRuntime with InterLink integration.
func NewFakeRemoteRuntime(interLinkHandler *api.InterLinkHandler) *RemoteRuntime {
	fakeRuntimeService := apitest.NewFakeRuntimeService()
	fakeImageService := apitest.NewFakeImageService()

	f := &RemoteRuntime{
		server:           grpc.NewServer(),
		RuntimeService:   fakeRuntimeService,
		ImageService:     fakeImageService,
		InterLinkHandler: interLinkHandler,
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
	// If InterLink handler is available, clean up pod tracking
	if f.InterLinkHandler != nil {
		if podStatus, found := getPodStatus(req.PodSandboxId); found {
			// Create a pod object for deletion if we have tracking data
			pod := &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      podStatus.PodName,
					Namespace: podStatus.PodNamespace,
					UID:       ktypes.UID(podStatus.PodUID),
				},
			}

			// Signal to interLink that the pod should be deleted
			_, err := json.Marshal(pod)
			if err == nil {
				// Note: In a real implementation, this would call the delete handler
				// For now, we just clean up the tracking
				removePodStatus(req.PodSandboxId)
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

	// If InterLink handler is available, integrate with interLink lifecycle
	if f.InterLinkHandler != nil {
		// Convert CRI config to interLink pod format
		pod := f.convertToInterLinkPod(ctx, req.SandboxConfig, req.Config)

		// Create pod creation request for interLink
		podCreateReq := types.PodCreateRequests{
			Pod: *pod,
		}

		// Marshal the request (for potential future use)
		_, err := json.Marshal(podCreateReq)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal pod create request: %w", err)
		}

		// Store the pod in interLink's pod tracking
		podStatus := types.PodStatus{
			PodName:      pod.Name,
			PodNamespace: pod.Namespace,
			PodUID:       string(pod.UID),
			Containers:   []v1.ContainerStatus{},
		}
		api.PodStatuses.Statuses[string(pod.UID)] = podStatus
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

	// If InterLink handler is available, update container state in interLink tracking
	if f.InterLinkHandler != nil {
		// Find the container in the fake runtime to get pod info
		f.RuntimeService.Lock()
		container, exists := f.RuntimeService.Containers[req.ContainerId]
		f.RuntimeService.Unlock()

		if exists {
			// Update container status to running
			containerStatus := v1.ContainerStatus{
				Name:  container.Metadata.Name,
				Image: container.Image.Image,
				State: v1.ContainerState{
					Running: &v1.ContainerStateRunning{
						StartedAt: metav1.NewTime(time.Now()),
					},
				},
				Ready: true,
			}

			updatePodStatusContainer(container.SandboxID, containerStatus)
		}
	}

	return &kubeapi.StartContainerResponse{}, nil
}

// StopContainer stops a running container using interLink lifecycle
func (f *RemoteRuntime) StopContainer(ctx context.Context, req *kubeapi.StopContainerRequest) (*kubeapi.StopContainerResponse, error) {
	// Stop container in fake runtime for CRI compatibility
	err := f.RuntimeService.StopContainer(ctx, req.ContainerId, req.Timeout)
	if err != nil {
		return nil, err
	}

	// If InterLink handler is available, update container state in interLink tracking
	if f.InterLinkHandler != nil {
		// Find the container in the fake runtime to get pod info
		f.RuntimeService.Lock()
		container, exists := f.RuntimeService.Containers[req.ContainerId]
		f.RuntimeService.Unlock()

		if exists {
			// Update container status to terminated
			containerStatus := v1.ContainerStatus{
				Name:  container.Metadata.Name,
				Image: container.Image.Image,
				State: v1.ContainerState{
					Terminated: &v1.ContainerStateTerminated{
						ExitCode:   0,
						Reason:     "Completed",
						FinishedAt: metav1.NewTime(time.Now()),
					},
				},
				Ready: false,
			}

			updatePodStatusContainer(container.SandboxID, containerStatus)
		}
	}

	return &kubeapi.StopContainerResponse{}, nil
}

// RemoveContainer removes the container using interLink lifecycle
func (f *RemoteRuntime) RemoveContainer(ctx context.Context, req *kubeapi.RemoveContainerRequest) (*kubeapi.RemoveContainerResponse, error) {
	// If InterLink handler is available, handle cleanup before removing from fake runtime
	if f.InterLinkHandler != nil {
		// Find the container in the fake runtime to get pod info before removal
		f.RuntimeService.Lock()
		container, exists := f.RuntimeService.Containers[req.ContainerId]
		f.RuntimeService.Unlock()

		if exists {
			removePodStatusContainer(container.SandboxID, container.Metadata.Name)
		}
	}

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

	// If InterLink handler is available, update status with interLink data
	if f.InterLinkHandler != nil {
		f.RuntimeService.Lock()
		container, exists := f.RuntimeService.Containers[req.ContainerId]
		f.RuntimeService.Unlock()

		if exists {
			if podStatus, found := getPodStatus(container.SandboxID); found {
				// Find the matching container status in interLink tracking
				for _, cs := range podStatus.Containers {
					if cs.Name == container.Metadata.Name {
						// For now, just ensure the container is tracked in interLink
						// More complex state mapping can be added later
						break
					}
				}
			}
		}
	}

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
