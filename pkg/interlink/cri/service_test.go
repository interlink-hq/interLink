package cri

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestNewFakeRuntimeService(t *testing.T) {
	service := NewFakeRuntimeService()

	assert.NotNil(t, service)
	assert.NotNil(t, service.Called)
	assert.NotNil(t, service.Errors)
	assert.NotNil(t, service.Containers)
	assert.NotNil(t, service.Sandboxes)
	assert.Len(t, service.Called, 0)
}

func TestFakeRuntimeService_Version(t *testing.T) {
	service := NewFakeRuntimeService()
	ctx := context.Background()

	resp, err := service.Version(ctx, "v1")
	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, FakeVersion, resp.Version)
	assert.Equal(t, FakeRuntimeName, resp.RuntimeName)
	assert.Contains(t, service.Called, "Version")
}

func TestFakeRuntimeService_Status(t *testing.T) {
	service := NewFakeRuntimeService()
	service.FakeStatus = &runtimeapi.RuntimeStatus{
		Conditions: []*runtimeapi.RuntimeCondition{
			{
				Type:   runtimeapi.RuntimeReady,
				Status: true,
			},
		},
	}
	ctx := context.Background()

	resp, err := service.Status(ctx, false)
	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.NotNil(t, resp.Status)
	assert.Contains(t, service.Called, "Status")
}

func TestFakeRuntimeService_RunPodSandbox(t *testing.T) {
	service := NewFakeRuntimeService()
	ctx := context.Background()

	config := &runtimeapi.PodSandboxConfig{
		Metadata: &runtimeapi.PodSandboxMetadata{
			Name:      "test-pod",
			Namespace: "default",
			Uid:       "12345",
		},
	}

	sandboxID, err := service.RunPodSandbox(ctx, config, "")
	require.NoError(t, err)
	assert.NotEmpty(t, sandboxID)
	assert.Contains(t, service.Sandboxes, sandboxID)
	assert.Contains(t, service.Called, "RunPodSandbox")
}

func TestFakeRuntimeService_StopPodSandbox(t *testing.T) {
	service := NewFakeRuntimeService()
	ctx := context.Background()

	// First create a sandbox
	config := &runtimeapi.PodSandboxConfig{
		Metadata: &runtimeapi.PodSandboxMetadata{
			Name:      "test-pod",
			Namespace: "default",
			Uid:       "12345",
		},
	}
	sandboxID, err := service.RunPodSandbox(ctx, config, "")
	require.NoError(t, err)

	// Stop the sandbox
	err = service.StopPodSandbox(ctx, sandboxID)
	require.NoError(t, err)

	// Verify sandbox is not ready
	sandbox := service.Sandboxes[sandboxID]
	assert.Equal(t, runtimeapi.PodSandboxState_SANDBOX_NOTREADY, sandbox.State)
	assert.Contains(t, service.Called, "StopPodSandbox")
}

func TestFakeRuntimeService_RemovePodSandbox(t *testing.T) {
	service := NewFakeRuntimeService()
	ctx := context.Background()

	// First create a sandbox
	config := &runtimeapi.PodSandboxConfig{
		Metadata: &runtimeapi.PodSandboxMetadata{
			Name:      "test-pod",
			Namespace: "default",
			Uid:       "12345",
		},
	}
	sandboxID, err := service.RunPodSandbox(ctx, config, "")
	require.NoError(t, err)

	// Remove the sandbox
	err = service.RemovePodSandbox(ctx, sandboxID)
	require.NoError(t, err)

	// Verify sandbox is removed
	_, exists := service.Sandboxes[sandboxID]
	assert.False(t, exists)
	assert.Contains(t, service.Called, "RemovePodSandbox")
}

func TestFakeRuntimeService_CreateContainer(t *testing.T) {
	service := NewFakeRuntimeService()
	ctx := context.Background()

	// First create a sandbox
	sandboxConfig := &runtimeapi.PodSandboxConfig{
		Metadata: &runtimeapi.PodSandboxMetadata{
			Name:      "test-pod",
			Namespace: "default",
			Uid:       "12345",
		},
	}
	sandboxID, err := service.RunPodSandbox(ctx, sandboxConfig, "")
	require.NoError(t, err)

	// Create a container
	containerConfig := &runtimeapi.ContainerConfig{
		Metadata: &runtimeapi.ContainerMetadata{
			Name: "test-container",
		},
		Image: &runtimeapi.ImageSpec{
			Image: "nginx:latest",
		},
	}

	containerID, err := service.CreateContainer(ctx, sandboxID, containerConfig, sandboxConfig)
	require.NoError(t, err)
	assert.NotEmpty(t, containerID)
	assert.Contains(t, service.Containers, containerID)
	assert.Equal(t, runtimeapi.ContainerState_CONTAINER_CREATED, service.Containers[containerID].State)
	assert.Contains(t, service.Called, "CreateContainer")
}

func TestFakeRuntimeService_StartContainer(t *testing.T) {
	service := NewFakeRuntimeService()
	ctx := context.Background()

	// Create sandbox and container first
	sandboxConfig := &runtimeapi.PodSandboxConfig{
		Metadata: &runtimeapi.PodSandboxMetadata{
			Name:      "test-pod",
			Namespace: "default",
			Uid:       "12345",
		},
	}
	sandboxID, err := service.RunPodSandbox(ctx, sandboxConfig, "")
	require.NoError(t, err)

	containerConfig := &runtimeapi.ContainerConfig{
		Metadata: &runtimeapi.ContainerMetadata{
			Name: "test-container",
		},
		Image: &runtimeapi.ImageSpec{
			Image: "nginx:latest",
		},
	}
	containerID, err := service.CreateContainer(ctx, sandboxID, containerConfig, sandboxConfig)
	require.NoError(t, err)

	// Start the container
	err = service.StartContainer(ctx, containerID)
	require.NoError(t, err)

	// Verify container is running
	assert.Equal(t, runtimeapi.ContainerState_CONTAINER_RUNNING, service.Containers[containerID].State)
	assert.NotZero(t, service.Containers[containerID].StartedAt)
	assert.Contains(t, service.Called, "StartContainer")
}

func TestFakeRuntimeService_StopContainer(t *testing.T) {
	service := NewFakeRuntimeService()
	ctx := context.Background()

	// Create and start a container
	sandboxConfig := &runtimeapi.PodSandboxConfig{
		Metadata: &runtimeapi.PodSandboxMetadata{
			Name:      "test-pod",
			Namespace: "default",
			Uid:       "12345",
		},
	}
	sandboxID, err := service.RunPodSandbox(ctx, sandboxConfig, "")
	require.NoError(t, err)

	containerConfig := &runtimeapi.ContainerConfig{
		Metadata: &runtimeapi.ContainerMetadata{
			Name: "test-container",
		},
		Image: &runtimeapi.ImageSpec{
			Image: "nginx:latest",
		},
	}
	containerID, err := service.CreateContainer(ctx, sandboxID, containerConfig, sandboxConfig)
	require.NoError(t, err)
	err = service.StartContainer(ctx, containerID)
	require.NoError(t, err)

	// Stop the container
	err = service.StopContainer(ctx, containerID, 10)
	require.NoError(t, err)

	// Verify container is exited
	assert.Equal(t, runtimeapi.ContainerState_CONTAINER_EXITED, service.Containers[containerID].State)
	assert.NotZero(t, service.Containers[containerID].FinishedAt)
	assert.Contains(t, service.Called, "StopContainer")
}

func TestFakeRuntimeService_InjectError(t *testing.T) {
	service := NewFakeRuntimeService()
	ctx := context.Background()

	// Inject an error for Version call
	testErr := assert.AnError
	service.InjectError("Version", testErr)

	_, err := service.Version(ctx, "v1")
	assert.Error(t, err)
	assert.Equal(t, testErr, err)
}

func TestFakeRuntimeService_ListPodSandbox(t *testing.T) {
	service := NewFakeRuntimeService()
	ctx := context.Background()

	// Create multiple sandboxes
	for i := 0; i < 3; i++ {
		config := &runtimeapi.PodSandboxConfig{
			Metadata: &runtimeapi.PodSandboxMetadata{
				Name:      "test-pod",
				Namespace: "default",
				Uid:       string(rune(i)),
			},
		}
		_, err := service.RunPodSandbox(ctx, config, "")
		require.NoError(t, err)
	}

	// List all sandboxes
	sandboxes, err := service.ListPodSandbox(ctx, nil)
	require.NoError(t, err)
	assert.Len(t, sandboxes, 3)
	assert.Contains(t, service.Called, "ListPodSandbox")
}

func TestFakeRuntimeService_ListContainers(t *testing.T) {
	service := NewFakeRuntimeService()
	ctx := context.Background()

	// Create sandbox and containers
	sandboxConfig := &runtimeapi.PodSandboxConfig{
		Metadata: &runtimeapi.PodSandboxMetadata{
			Name:      "test-pod",
			Namespace: "default",
			Uid:       "12345",
		},
	}
	sandboxID, err := service.RunPodSandbox(ctx, sandboxConfig, "")
	require.NoError(t, err)

	for i := 0; i < 2; i++ {
		containerConfig := &runtimeapi.ContainerConfig{
			Metadata: &runtimeapi.ContainerMetadata{
				Name:    "test-container",
				Attempt: uint32(i),
			},
			Image: &runtimeapi.ImageSpec{
				Image: "nginx:latest",
			},
		}
		_, err := service.CreateContainer(ctx, sandboxID, containerConfig, sandboxConfig)
		require.NoError(t, err)
	}

	// List all containers
	containers, err := service.ListContainers(ctx, nil)
	require.NoError(t, err)
	assert.Len(t, containers, 2)
	assert.Contains(t, service.Called, "ListContainers")
}
