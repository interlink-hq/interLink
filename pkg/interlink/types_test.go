package interlink

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPodCreateRequests_JSONSerialization(t *testing.T) {
	pod := v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
			UID:       "12345-67890",
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name:  "test-container",
					Image: "nginx:latest",
				},
			},
		},
	}

	configMap := v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-config",
			Namespace: "default",
		},
		Data: map[string]string{
			"key": "value",
		},
	}

	secret := v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"password": []byte("secret-value"),
		},
	}

	request := PodCreateRequests{
		Pod:                 pod,
		ConfigMaps:          []v1.ConfigMap{configMap},
		Secrets:             []v1.Secret{secret},
		ProjectedVolumeMaps: []v1.ConfigMap{},
		JobScriptBuilderURL: "http://builder.example.com",
	}

	// Serialize to JSON
	data, err := json.Marshal(request)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Deserialize from JSON
	var decoded PodCreateRequests
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, request.Pod.Name, decoded.Pod.Name)
	assert.Equal(t, request.Pod.Namespace, decoded.Pod.Namespace)
	assert.Len(t, decoded.ConfigMaps, 1)
	assert.Equal(t, "test-config", decoded.ConfigMaps[0].Name)
	assert.Len(t, decoded.Secrets, 1)
	assert.Equal(t, "test-secret", decoded.Secrets[0].Name)
	assert.Equal(t, request.JobScriptBuilderURL, decoded.JobScriptBuilderURL)
}

func TestPodStatus_JSONSerialization(t *testing.T) {
	containerStatus := v1.ContainerStatus{
		Name:  "test-container",
		Ready: true,
		State: v1.ContainerState{
			Running: &v1.ContainerStateRunning{
				StartedAt: metav1.Time{Time: time.Now()},
			},
		},
	}

	podStatus := PodStatus{
		PodName:      "test-pod",
		PodUID:       "12345-67890",
		PodNamespace: "default",
		JobID:        "slurm-123456",
		Containers:   []v1.ContainerStatus{containerStatus},
	}

	// Serialize to JSON
	data, err := json.Marshal(podStatus)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Deserialize from JSON
	var decoded PodStatus
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, podStatus.PodName, decoded.PodName)
	assert.Equal(t, podStatus.PodUID, decoded.PodUID)
	assert.Equal(t, podStatus.PodNamespace, decoded.PodNamespace)
	assert.Equal(t, podStatus.JobID, decoded.JobID)
	assert.Len(t, decoded.Containers, 1)
	assert.Equal(t, "test-container", decoded.Containers[0].Name)
}

func TestCreateStruct_JSONSerialization(t *testing.T) {
	createStruct := CreateStruct{
		PodUID: "pod-uuid-12345",
		PodJID: "slurm-job-67890",
	}

	// Serialize to JSON
	data, err := json.Marshal(createStruct)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Deserialize from JSON
	var decoded CreateStruct
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, createStruct.PodUID, decoded.PodUID)
	assert.Equal(t, createStruct.PodJID, decoded.PodJID)
}

func TestRetrievedPodData_JSONSerialization(t *testing.T) {
	pod := v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "retrieved-pod",
			Namespace: "default",
		},
	}

	container := RetrievedContainer{
		Name: "main-container",
		ConfigMaps: []v1.ConfigMap{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "config1"},
				Data:       map[string]string{"key": "value"},
			},
		},
		Secrets: []v1.Secret{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "secret1"},
				Data:       map[string][]byte{"password": []byte("secret")},
			},
		},
		EmptyDirs: []string{"/tmp/empty1", "/tmp/empty2"},
	}

	jobScriptConfig := ScriptBuildConfig{
		SingularityHub: SingularityHubConfig{
			Server:      "https://hub.example.com",
			MasterToken: "token123",
		},
		ApptainerOptions: ApptainerOptions{
			Executable: "/usr/bin/apptainer",
			Fakeroot:   true,
		},
	}

	retrievedPod := RetrievedPodData{
		Pod:            pod,
		Containers:     []RetrievedContainer{container},
		JobScriptBuild: jobScriptConfig,
		JobScript:      "#!/bin/bash\necho 'test'",
	}

	// Serialize to JSON
	data, err := json.Marshal(retrievedPod)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Deserialize from JSON
	var decoded RetrievedPodData
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, retrievedPod.Pod.Name, decoded.Pod.Name)
	assert.Len(t, decoded.Containers, 1)
	assert.Equal(t, "main-container", decoded.Containers[0].Name)
	assert.Len(t, decoded.Containers[0].ConfigMaps, 1)
	assert.Len(t, decoded.Containers[0].Secrets, 1)
	assert.Len(t, decoded.Containers[0].EmptyDirs, 2)
	assert.Equal(t, retrievedPod.JobScript, decoded.JobScript)
}

func TestLogStruct_JSONSerialization(t *testing.T) {
	logOpts := ContainerLogOpts{
		Tail:         100,
		LimitBytes:   1024,
		Timestamps:   true,
		Follow:       false,
		Previous:     false,
		SinceSeconds: 3600,
		SinceTime:    time.Now(),
	}

	logStruct := LogStruct{
		Namespace:     "default",
		PodUID:        "pod-12345",
		PodName:       "test-pod",
		ContainerName: "test-container",
		Opts:          logOpts,
	}

	// Serialize to JSON
	data, err := json.Marshal(logStruct)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Deserialize from JSON
	var decoded LogStruct
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, logStruct.Namespace, decoded.Namespace)
	assert.Equal(t, logStruct.PodUID, decoded.PodUID)
	assert.Equal(t, logStruct.PodName, decoded.PodName)
	assert.Equal(t, logStruct.ContainerName, decoded.ContainerName)
	assert.Equal(t, logStruct.Opts.Tail, decoded.Opts.Tail)
	assert.Equal(t, logStruct.Opts.LimitBytes, decoded.Opts.LimitBytes)
	assert.Equal(t, logStruct.Opts.Timestamps, decoded.Opts.Timestamps)
	assert.Equal(t, logStruct.Opts.Follow, decoded.Opts.Follow)
}

func TestContainerLogOpts_DefaultValues(t *testing.T) {
	// Test zero values
	opts := ContainerLogOpts{}

	assert.Equal(t, 0, opts.Tail)
	assert.Equal(t, 0, opts.LimitBytes)
	assert.False(t, opts.Timestamps)
	assert.False(t, opts.Follow)
	assert.False(t, opts.Previous)
	assert.Equal(t, 0, opts.SinceSeconds)
}

func TestRetrievedContainer_EmptyDirsDeprecation(t *testing.T) {
	// Test that EmptyDirs field still works (backwards compatibility)
	container := RetrievedContainer{
		Name:      "test",
		EmptyDirs: []string{"/path1", "/path2"},
	}

	data, err := json.Marshal(container)
	require.NoError(t, err)

	var decoded RetrievedContainer
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, container.EmptyDirs, decoded.EmptyDirs)
	assert.Len(t, decoded.EmptyDirs, 2)
}

func TestPodStatus_MultipleContainers(t *testing.T) {
	podStatus := PodStatus{
		PodName:      "multi-container-pod",
		PodUID:       "uuid-123",
		PodNamespace: "default",
		JobID:        "job-456",
		Containers: []v1.ContainerStatus{
			{Name: "container1", Ready: true},
			{Name: "container2", Ready: false},
		},
		InitContainers: []v1.ContainerStatus{
			{Name: "init1", Ready: true},
		},
	}

	data, err := json.Marshal(podStatus)
	require.NoError(t, err)

	var decoded PodStatus
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Len(t, decoded.Containers, 2)
	assert.Len(t, decoded.InitContainers, 1)
	assert.Equal(t, "container1", decoded.Containers[0].Name)
	assert.Equal(t, "init1", decoded.InitContainers[0].Name)
}
