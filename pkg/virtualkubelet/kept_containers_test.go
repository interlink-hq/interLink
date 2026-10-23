package virtualkubelet

import (
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	types "github.com/interlink-hq/interlink/pkg/interlink"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	"sigs.k8s.io/yaml"
)

const keptInitName = "stage-in"

// podKeepingInit is an offloaded pod whose first init container stays on the cluster.
func podKeepingInit(policy v1.RestartPolicy) *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "trainer", Namespace: testNamespaceDefault, UID: k8stypes.UID("uid-kept"),
			Annotations: map[string]string{annDisableOffloadInitContainers: keptInitName},
		},
		Spec: v1.PodSpec{
			RestartPolicy: policy,
			InitContainers: []v1.Container{
				{Name: keptInitName, Image: "rsync"},
				{Name: "warmup", Image: "app"},
			},
			Containers: []v1.Container{{Name: testAppName, Image: "app"}},
		},
	}
}

// shadowOf returns the shadow pod of pod with the given status for the kept init container.
func shadowOf(t *testing.T, pod *v1.Pod, status v1.ContainerStatus) *v1.Pod {
	t.Helper()
	identity, err := computeShadowResourceIdentity(pod)
	require.NoError(t, err)
	status.Name = keptInitName
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: identity.Name + "-abc", Namespace: identity.Namespace,
			Labels: map[string]string{shadowComponentLabel: identity.Name},
		},
		Status: v1.PodStatus{InitContainerStatuses: []v1.ContainerStatus{status}},
	}
}

func exited(code int32) v1.ContainerStatus {
	return v1.ContainerStatus{State: v1.ContainerState{Terminated: &v1.ContainerStateTerminated{ExitCode: code}}}
}

func keptProvider(client *fake.Clientset, pod *v1.Pod) *Provider {
	return &Provider{
		clientSet: client,
		notifier:  func(*v1.Pod) {},
		pods:      map[string]*v1.Pod{string(pod.UID): pod},
	}
}

func TestHasShadowForKeptContainers(t *testing.T) {
	p := &Provider{config: Config{Network: Network{EnableTunnel: false}}}
	pod := podKeepingInit(v1.RestartPolicyNever)

	assert.True(t, p.hasShadow(pod), "kept containers only run in a shadow, tunnel or not")
	assert.False(t, p.hasTunnelShadow(pod))

	delete(pod.Annotations, annDisableOffloadInitContainers)
	assert.False(t, p.hasShadow(pod), "nothing kept, no ports: no shadow")
}

func TestKeptShadowTemplate(t *testing.T) {
	// A custom tunnel template must not be used for a shadow that has no tunnel.
	p := &Provider{config: Config{Network: Network{WstunnelTemplatePath: "/nonexistent/custom.yaml"}}}
	data := ShadowTemplateData{
		Name: "shadow-trainer", Namespace: testNamespaceDefault, KeptOnly: true,
		PauseImage: p.shadowPauseImage(), PauseName: shadowPauseBaseName,
	}
	out, err := p.executeShadowTemplate(t.Context(), data)
	require.NoError(t, err)
	assert.NotContains(t, out, "kind: Service")

	var obj map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(out), &obj))
	assert.Equal(t, "Deployment", obj["kind"])
	assert.Contains(t, out, "name: "+shadowPauseBaseName+"\n        image: "+defaultShadowPauseImage)
	assert.Contains(t, out, "kubernetes.io/os: linux", "a shadow must never land on the virtual node")

	// Kept regular containers keep the shadow running: no pause container.
	data.LocalContainers = []v1.Container{{Name: shadowPauseBaseName, Image: "sidecar"}}
	out, err = p.executeShadowTemplate(t.Context(), data)
	require.NoError(t, err)
	assert.NotContains(t, out, defaultShadowPauseImage)
}

func TestShadowPauseName(t *testing.T) {
	assert.Equal(t, shadowPauseBaseName, shadowPauseName([]v1.Container{{Name: keptInitName}}))
	assert.Equal(t, "pause-2", shadowPauseName([]v1.Container{{Name: shadowPauseBaseName}, {Name: "pause-1"}}))
}

func TestKeptInitProgress(t *testing.T) {
	kept := []v1.Container{{Name: keptInitName}}
	running := v1.ContainerStatus{State: v1.ContainerState{Running: &v1.ContainerStateRunning{}}}
	retrying := v1.ContainerStatus{
		RestartCount:         1,
		State:                v1.ContainerState{Waiting: &v1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
		LastTerminationState: v1.ContainerState{Terminated: &v1.ContainerStateTerminated{ExitCode: 3}},
	}
	pod := podKeepingInit(v1.RestartPolicyNever)

	cases := []struct {
		name       string
		status     *v1.ContainerStatus
		done       bool
		failedCode bool
	}{
		{"not started", nil, false, false},
		{"running", &running, false, false},
		{"completed", ptrTo(exited(0)), true, false},
		{"exited non-zero", ptrTo(exited(2)), false, true},
		{"failed and being retried", &retrying, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			shadow := &v1.Pod{}
			if c.status != nil {
				shadow = shadowOf(t, pod, *c.status)
			}
			state := keptInitProgress(shadow, kept)
			assert.Equal(t, c.done, state.done)
			assert.Equal(t, c.failedCode, state.failed != nil)
		})
	}
}

func ptrTo[T any](v T) *T { return &v }

func TestKeptInitProgressSidecar(t *testing.T) {
	kept := []v1.Container{{Name: keptInitName, RestartPolicy: ptrTo(v1.ContainerRestartPolicyAlways)}}
	pod := podKeepingInit(v1.RestartPolicyNever)
	running := func(started bool) v1.ContainerStatus {
		return v1.ContainerStatus{Started: ptrTo(started), State: v1.ContainerState{Running: &v1.ContainerStateRunning{}}}
	}
	restarted := running(true)
	restarted.RestartCount = 1
	restarted.LastTerminationState = v1.ContainerState{Terminated: &v1.ContainerStateTerminated{ExitCode: 1}}

	state := keptInitProgress(shadowOf(t, pod, running(false)), kept)
	assert.False(t, state.done, "a sidecar that has not started yet")

	state = keptInitProgress(shadowOf(t, pod, running(true)), kept)
	assert.True(t, state.done, "a started sidecar keeps running next to the job")

	state = keptInitProgress(shadowOf(t, pod, restarted), kept)
	assert.True(t, state.done)
	assert.Nil(t, state.failed, "the shadow's kubelet restarts a sidecar, whatever the pod's restartPolicy")
}

func TestWaitForKeptInitContainers(t *testing.T) {
	keptInitPollInterval = 10 * time.Millisecond
	t.Cleanup(func() { keptInitPollInterval = 2 * time.Second })

	t.Run("submits once the kept init container completed, with its status mirrored", func(t *testing.T) {
		pod := podKeepingInit(v1.RestartPolicyNever)
		client := fake.NewSimpleClientset(shadowOf(t, pod, exited(0)))
		p := keptProvider(client, pod)

		require.NoError(t, p.waitForKeptInitContainers(p.trackKeptInitWait(t.Context(), pod), pod))
		require.Len(t, pod.Status.InitContainerStatuses, 1)
		assert.Equal(t, keptInitName, pod.Status.InitContainerStatuses[0].Name)
	})

	t.Run("a failure ends a restartPolicy Never pod without submitting", func(t *testing.T) {
		pod := podKeepingInit(v1.RestartPolicyNever)
		client := fake.NewSimpleClientset(shadowOf(t, pod, exited(2)))
		p := keptProvider(client, pod)

		require.Error(t, p.waitForKeptInitContainers(p.trackKeptInitWait(t.Context(), pod), pod))
		assert.Equal(t, v1.PodFailed, pod.Status.Phase)
		assert.Equal(t, "InitContainerFailed", pod.Status.Reason)
	})

	t.Run("a failure is retried for restartPolicy OnFailure until it succeeds", func(t *testing.T) {
		pod := podKeepingInit(v1.RestartPolicyOnFailure)
		shadow := shadowOf(t, pod, exited(2))
		client := fake.NewSimpleClientset(shadow)
		p := keptProvider(client, pod)

		ctx := p.trackKeptInitWait(t.Context(), pod)
		done := make(chan error, 1)
		go func() { done <- p.waitForKeptInitContainers(ctx, pod) }()

		select {
		case err := <-done:
			t.Fatalf("returned while the init container was failing: %v", err)
		case <-time.After(100 * time.Millisecond):
		}
		assert.NotEqual(t, v1.PodFailed, pod.Status.Phase)

		shadow.Status.InitContainerStatuses[0] = exited(0)
		shadow.Status.InitContainerStatuses[0].Name = keptInitName
		_, err := client.CoreV1().Pods(shadow.Namespace).UpdateStatus(t.Context(), shadow, metav1.UpdateOptions{})
		require.NoError(t, err)

		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(2 * time.Second):
			t.Fatal("still waiting after the init container succeeded")
		}
	})

	t.Run("stops when the pod is deleted while waiting", func(t *testing.T) {
		pod := podKeepingInit(v1.RestartPolicyNever)
		client := fake.NewSimpleClientset(shadowOf(t, pod, v1.ContainerStatus{
			State: v1.ContainerState{Running: &v1.ContainerStateRunning{}},
		}))
		p := keptProvider(client, pod)

		ctx := p.trackKeptInitWait(t.Context(), pod)
		done := make(chan error, 1)
		go func() { done <- p.waitForKeptInitContainers(ctx, pod) }()
		time.Sleep(50 * time.Millisecond)
		p.podsMu.Lock()
		delete(p.pods, string(pod.UID))
		p.podsMu.Unlock()

		select {
		case err := <-done:
			assert.ErrorIs(t, err, errPodGoneWhileInitializing)
		case <-time.After(2 * time.Second):
			t.Fatal("still waiting for a deleted pod")
		}
	})

	t.Run("DeletePod stops the wait, even while the remote deletion fails", func(t *testing.T) {
		var deletes atomic.Int32
		var method, path atomic.Value
		p, _ := newDeleteTestProvider(t, deleteHandler(&deletes, &method, &path, http.StatusInternalServerError))
		pod := podKeepingInit(v1.RestartPolicyNever)
		shadow := shadowOf(t, pod, v1.ContainerStatus{State: v1.ContainerState{Running: &v1.ContainerStateRunning{}}})
		client := fake.NewSimpleClientset(shadow)
		p.clientSet = client
		p.pods = map[string]*v1.Pod{string(pod.UID): pod}

		ctx := p.trackKeptInitWait(t.Context(), pod)
		done := make(chan error, 1)
		go func() { done <- p.waitForKeptInitContainers(ctx, pod) }()
		time.Sleep(50 * time.Millisecond)

		require.Error(t, p.DeletePod(t.Context(), pod))
		require.True(t, podIsTracked(p, string(pod.UID)), "the failed remote deletion keeps the pod tracked")
		// The init container completing now must not submit the pod.
		shadow.Status.InitContainerStatuses[0] = exited(0)
		shadow.Status.InitContainerStatuses[0].Name = keptInitName
		_, err := client.CoreV1().Pods(shadow.Namespace).UpdateStatus(t.Context(), shadow, metav1.UpdateOptions{})
		require.NoError(t, err)

		select {
		case err := <-done:
			assert.ErrorIs(t, err, errPodGoneWhileInitializing)
		case <-time.After(2 * time.Second):
			t.Fatal("still waiting for a pod being deleted")
		}
		assert.Equal(t, "/delete", path.Load())
	})

	t.Run("no kept init container: nothing to wait for", func(t *testing.T) {
		pod := podKeepingInit(v1.RestartPolicyNever)
		delete(pod.Annotations, annDisableOffloadInitContainers)
		p := keptProvider(fake.NewSimpleClientset(), pod)
		require.NoError(t, p.waitForKeptInitContainers(t.Context(), pod))
	})
}

func TestDropKeptOnlyVolumes(t *testing.T) {
	const dataVolume = "data"
	pod := podKeepingInit(v1.RestartPolicyNever)
	pod.Spec.InitContainers[0].VolumeMounts = []v1.VolumeMount{{Name: "credentials"}, {Name: dataVolume}}
	pod.Spec.Containers[0].VolumeMounts = []v1.VolumeMount{{Name: dataVolume}}
	pod.Spec.Volumes = []v1.Volume{{Name: "credentials"}, {Name: dataVolume}, {Name: "unused"}}

	offloaded := pod.DeepCopy()
	offloaded.Spec.Containers = getOffloadContainers(pod)
	offloaded.Spec.InitContainers = getOffloadInitContainers(pod)

	var names []string
	for _, v := range dropKeptOnlyVolumes(pod, offloaded) {
		names = append(names, v.Name)
	}
	assert.Equal(t, []string{dataVolume, "unused"}, names,
		"a volume only the kept init container mounts stays on the cluster; shared and unmounted ones are sent as before")
}

// The kept init container's status is already on the pod when the remote side first
// reports an offloaded one, which must be added after it, not written over it.
func TestRemoteInitStatusAppendedAfterKeptOne(t *testing.T) {
	pod := podKeepingInit(v1.RestartPolicyNever)
	kept := exited(0)
	kept.Name = keptInitName
	pod.Status.InitContainerStatuses = []v1.ContainerStatus{kept}

	remote := types.PodStatus{PodName: pod.Name, InitContainers: []v1.ContainerStatus{{
		Name: "warmup", State: v1.ContainerState{Running: &v1.ContainerStateRunning{}},
	}}}
	handleInitContainersUpdate(t.Context(), remote, pod, 1)

	require.Len(t, pod.Status.InitContainerStatuses, 2)
	assert.Equal(t, kept, pod.Status.InitContainerStatuses[0])
	assert.Equal(t, "warmup", pod.Status.InitContainerStatuses[1].Name)
	assert.NotNil(t, pod.Status.InitContainerStatuses[1].State.Running)
}
