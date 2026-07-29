package virtualkubelet

import (
	"testing"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// podWithPort is an offloaded pod that exposes a port, which is what makes the VK
// render a shadow for it.
func podWithPort(name, namespace string, uid string) *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, UID: k8stypes.UID(uid)},
		Spec: v1.PodSpec{
			Containers: []v1.Container{{
				Name:  "app",
				Ports: []v1.ContainerPort{{ContainerPort: 8888}},
			}},
		},
	}
}

func tunnelProvider(client *fake.Clientset) *Provider {
	return &Provider{clientSet: client, config: Config{Network: Network{EnableTunnel: true}}}
}

// configMapPatches returns the ConfigMap patch calls recorded by the fake client,
// which is how these tests tell "wrote to the API" from "decided not to".
func configMapPatches(client *fake.Clientset) []k8stesting.Action {
	var patches []k8stesting.Action
	for _, a := range client.Actions() {
		if a.Matches("patch", "configmaps") {
			patches = append(patches, a)
		}
	}
	return patches
}

func TestResetShadowNodeConfigMap(t *testing.T) {
	identity := shadowResourceIdentity{Name: "pod-default", Namespace: testNamespaceDefault}

	t.Run("creates the configmap with an empty node so the shadow can mount it before the job is scheduled", func(t *testing.T) {
		client := fake.NewSimpleClientset()
		p := tunnelProvider(client)

		assert.NoError(t, p.resetShadowNodeConfigMap(t.Context(), identity))

		cm, err := client.CoreV1().ConfigMaps(identity.Namespace).Get(t.Context(), shadowNodeConfigMapName(identity.Name), metav1.GetOptions{})
		assert.NoError(t, err)
		assert.Equal(t, "", cm.Data[shadowNodeNameKey])
	})

	t.Run("blanks a leftover node from a previous run", func(t *testing.T) {
		client := fake.NewSimpleClientset(&v1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: shadowNodeConfigMapName(identity.Name), Namespace: identity.Namespace},
			Data:       map[string]string{shadowNodeNameKey: "stale-node.hpc.example.org"},
		})
		p := tunnelProvider(client)

		assert.NoError(t, p.resetShadowNodeConfigMap(t.Context(), identity))

		cm, err := client.CoreV1().ConfigMaps(identity.Namespace).Get(t.Context(), shadowNodeConfigMapName(identity.Name), metav1.GetOptions{})
		assert.NoError(t, err)
		assert.Equal(t, "", cm.Data[shadowNodeNameKey],
			"a stale node would make the shadow tunnel to a host whose job is gone")
	})
}

func TestPublishShadowNodeName(t *testing.T) {
	pod := podWithPort("nb", testNamespaceDefault, "uid-1")
	identity, err := computeShadowResourceIdentity(pod)
	assert.NoError(t, err)
	cmName := shadowNodeConfigMapName(identity.Name)

	newClient := func() *fake.Clientset {
		return fake.NewSimpleClientset(&v1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: cmName, Namespace: identity.Namespace},
			Data:       map[string]string{shadowNodeNameKey: ""},
		})
	}

	t.Run("writes the node reported by the plugin", func(t *testing.T) {
		client := newClient()
		p := tunnelProvider(client)

		p.publishShadowNodeName(t.Context(), pod, "node042.hpc.example.org")

		cm, err := client.CoreV1().ConfigMaps(identity.Namespace).Get(t.Context(), cmName, metav1.GetOptions{})
		assert.NoError(t, err)
		assert.Equal(t, "node042.hpc.example.org", cm.Data[shadowNodeNameKey])
	})

	t.Run("an empty node name leaves the configmap alone while the job is queued", func(t *testing.T) {
		client := newClient()
		p := tunnelProvider(client)

		p.publishShadowNodeName(t.Context(), pod, "")

		assert.Empty(t, configMapPatches(client), "waiting for an allocation must not write to the API")
	})

	t.Run("repeated reports of the same node do not write again", func(t *testing.T) {
		client := newClient()
		p := tunnelProvider(client)

		p.publishShadowNodeName(t.Context(), pod, "node042.hpc.example.org")
		before := len(configMapPatches(client))
		for range 5 {
			p.publishShadowNodeName(t.Context(), pod, "node042.hpc.example.org")
		}

		assert.Len(t, configMapPatches(client), before,
			"the status loop polls continuously; only changes should reach the API")
	})

	t.Run("a pod with no shadow is skipped", func(t *testing.T) {
		client := newClient()
		p := &Provider{clientSet: client, config: Config{Network: Network{EnableTunnel: false}}}

		p.publishShadowNodeName(t.Context(), pod, "node042.hpc.example.org")

		assert.Empty(t, configMapPatches(client))
	})

	t.Run("forgetting a pod lets the same node be published again", func(t *testing.T) {
		client := newClient()
		p := tunnelProvider(client)

		p.publishShadowNodeName(t.Context(), pod, "node042.hpc.example.org")
		p.forgetShadowNodeName(pod)
		p.publishShadowNodeName(t.Context(), pod, "node042.hpc.example.org")

		assert.Len(t, configMapPatches(client), 2)
	})
}

func TestCleanupShadowResourcesRemovesNodeConfigMap(t *testing.T) {
	const name = "pod-default"
	ns := testNamespaceDefault

	client := fake.NewSimpleClientset(&v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: shadowNodeConfigMapName(name), Namespace: ns},
	})
	p := tunnelProvider(client)

	p.cleanupShadowResources(t.Context(), name, ns)

	_, err := client.CoreV1().ConfigMaps(ns).Get(t.Context(), shadowNodeConfigMapName(name), metav1.GetOptions{})
	assert.True(t, apierrors.IsNotFound(err), "the node configmap should be deleted with the rest of the shadow")
}
