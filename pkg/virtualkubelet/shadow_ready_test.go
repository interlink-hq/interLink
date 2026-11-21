package virtualkubelet

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
)

// The shadow reads the node from a ConfigMap volume, which the kubelet refreshes
// when the pod changes: the shadow's pods are annotated with the node.
func TestPublishShadowNodeTouchesShadowPods(t *testing.T) {
	pod := podWithPort("nb", testNamespaceDefault, "uid-1")
	identity, err := computeShadowResourceIdentity(pod)
	require.NoError(t, err)
	current := shadowPodOf(t, pod, "a", "10.0.0.1", time.Now())
	previous := shadowPodOf(t, podWithPort("nb", testNamespaceDefault, "uid-0"), "b", "10.0.0.2", time.Now())
	client := fake.NewSimpleClientset(&v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: shadowNodeConfigMapName(identity.Name), Namespace: identity.Namespace},
		Data:       map[string]string{shadowNodeNameKey: ""},
	}, current, previous)

	tunnelProvider(client).publishShadowNode(t.Context(), pod, testComputeNode, "")

	got, err := client.CoreV1().Pods(identity.Namespace).Get(t.Context(), current.Name, metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, testComputeNode, got.Annotations[shadowNodeAnnotation])
	other, err := client.CoreV1().Pods(identity.Namespace).Get(t.Context(), previous.Name, metav1.GetOptions{})
	require.NoError(t, err)
	assert.NotContains(t, other.Annotations, shadowNodeAnnotation, "the shadow pod of a previous pod is left alone")
}

func TestShadowServing(t *testing.T) {
	pod := podWithPort("nb", testNamespaceDefault, "uid-1")
	ready := shadowPodOf(t, pod, "a", "10.0.0.1", time.Now())
	unready := shadowPodOf(t, pod, "b", "10.0.0.2", time.Now())
	unready.Status.Conditions = []v1.PodCondition{{Type: v1.PodReady, Status: v1.ConditionFalse}}

	t.Run("a pod without ports is not served through a shadow", func(t *testing.T) {
		portless := &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "job", Namespace: testNamespaceDefault, UID: k8stypes.UID("uid-2")},
			Spec:       v1.PodSpec{Containers: []v1.Container{{Name: testAppName}}},
		}
		assert.True(t, tunnelProvider(fake.NewSimpleClientset()).shadowServing(t.Context(), portless))
	})

	t.Run("no shadow pod yet", func(t *testing.T) {
		assert.False(t, tunnelProvider(fake.NewSimpleClientset()).shadowServing(t.Context(), pod))
	})

	t.Run("the shadow is not ready", func(t *testing.T) {
		assert.False(t, tunnelProvider(fake.NewSimpleClientset(unready)).shadowServing(t.Context(), pod))
	})

	t.Run("the shadow is ready", func(t *testing.T) {
		assert.True(t, tunnelProvider(fake.NewSimpleClientset(ready)).shadowServing(t.Context(), pod))
	})
}

func TestSetReadyFromShadow(t *testing.T) {
	running := func() *v1.Pod {
		return &v1.Pod{Status: v1.PodStatus{
			Conditions:        []v1.PodCondition{{Type: v1.PodReady, Status: v1.ConditionTrue}},
			ContainerStatuses: []v1.ContainerStatus{{Name: testAppName, Ready: true}},
		}}
	}
	readyCondition := func(pod *v1.Pod) v1.PodCondition {
		require.Len(t, pod.Status.Conditions, 1)
		return pod.Status.Conditions[0]
	}

	t.Run("not ready while the shadow does not serve", func(t *testing.T) {
		pod := running()
		setReadyFromShadow(pod, false)

		condition := readyCondition(pod)
		assert.Equal(t, v1.ConditionFalse, condition.Status)
		assert.Equal(t, "ShadowNotReady", condition.Reason)
		assert.False(t, pod.Status.ContainerStatuses[0].Ready)
	})

	t.Run("ready once it serves", func(t *testing.T) {
		pod := running()
		setReadyFromShadow(pod, false)
		setReadyFromShadow(pod, true)

		condition := readyCondition(pod)
		assert.Equal(t, v1.ConditionTrue, condition.Status)
		assert.Empty(t, condition.Reason)
	})

	t.Run("keeps the transition time while nothing changes", func(t *testing.T) {
		pod := running()
		since := metav1.NewTime(time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC))
		pod.Status.Conditions[0].LastTransitionTime = since
		setReadyFromShadow(pod, true)

		assert.Equal(t, since, readyCondition(pod).LastTransitionTime)
	})
}
