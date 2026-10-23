package virtualkubelet

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

const (
	testComputeNode = "node042.hpc.example.org"
	testSocketDir   = "/shared/il/0123456789ab"
)

// podWithPort is an offloaded pod that exposes a port, which is what makes the VK
// render a shadow for it.
func podWithPort(name, namespace string, uid string) *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, UID: k8stypes.UID(uid)},
		Spec: v1.PodSpec{
			Containers: []v1.Container{{
				Name:  testAppName,
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
	identity := shadowResourceIdentity{Name: testPodDefaultName, Namespace: testNamespaceDefault}

	t.Run("creates the configmap with an empty node so the shadow can mount it before the job is scheduled", func(t *testing.T) {
		client := fake.NewSimpleClientset()
		p := tunnelProvider(client)

		assert.NoError(t, p.resetShadowNodeConfigMap(t.Context(), identity))

		cm, err := client.CoreV1().ConfigMaps(identity.Namespace).Get(t.Context(), shadowNodeConfigMapName(identity.Name), metav1.GetOptions{})
		assert.NoError(t, err)
		assert.Equal(t, map[string]string{shadowNodeNameKey: "", shadowSocketDirKey: ""}, cm.Data)
	})

	t.Run("blanks a leftover node and socket directory from a previous run", func(t *testing.T) {
		client := fake.NewSimpleClientset(&v1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: shadowNodeConfigMapName(identity.Name), Namespace: identity.Namespace},
			Data:       map[string]string{shadowNodeNameKey: "stale-node.hpc.example.org", shadowSocketDirKey: testSocketDir},
		})
		p := tunnelProvider(client)

		assert.NoError(t, p.resetShadowNodeConfigMap(t.Context(), identity))

		cm, err := client.CoreV1().ConfigMaps(identity.Namespace).Get(t.Context(), shadowNodeConfigMapName(identity.Name), metav1.GetOptions{})
		assert.NoError(t, err)
		assert.Equal(t, "", cm.Data[shadowNodeNameKey],
			"a stale node would make the shadow tunnel to a host whose job is gone")
		assert.Equal(t, "", cm.Data[shadowSocketDirKey])
	})

	// The cleanup of a deleted pod with the same name removes its ConfigMap between
	// this Create (AlreadyExists) and the Update.
	t.Run("creates it again when it is deleted before the update", func(t *testing.T) {
		const previousOwner, owner = "uid-previous", "uid-current"
		client := fake.NewSimpleClientset(&v1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
			Name: shadowNodeConfigMapName(identity.Name), Namespace: identity.Namespace,
			Labels: map[string]string{shadowOwnerUIDLabel: previousOwner},
		}})
		gvr := v1.SchemeGroupVersion.WithResource("configmaps")
		client.PrependReactor("update", "configmaps", func(k8stesting.Action) (bool, runtime.Object, error) {
			require.NoError(t, client.Tracker().Delete(gvr, identity.Namespace, shadowNodeConfigMapName(identity.Name)))
			return true, nil, apierrors.NewNotFound(gvr.GroupResource(), shadowNodeConfigMapName(identity.Name))
		})
		p := tunnelProvider(client)
		current := identity
		current.OwnerUID = owner

		require.NoError(t, p.resetShadowNodeConfigMap(t.Context(), current))

		cm, err := client.CoreV1().ConfigMaps(identity.Namespace).Get(t.Context(), shadowNodeConfigMapName(identity.Name), metav1.GetOptions{})
		require.NoError(t, err)
		assert.Equal(t, owner, cm.Labels[shadowOwnerUIDLabel])
	})
}

func TestPublishShadowNode(t *testing.T) {
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

		p.publishShadowNode(t.Context(), pod, testComputeNode, "")

		cm, err := client.CoreV1().ConfigMaps(identity.Namespace).Get(t.Context(), cmName, metav1.GetOptions{})
		assert.NoError(t, err)
		assert.Equal(t, testComputeNode, cm.Data[shadowNodeNameKey])
	})

	// The shadow must never see a new node next to the previous job's directory.
	t.Run("writes the node and the socket directory in one patch", func(t *testing.T) {
		client := newClient()
		p := tunnelProvider(client)

		p.publishShadowNode(t.Context(), pod, testComputeNode, testSocketDir)

		assert.Len(t, configMapPatches(client), 1)
		cm, err := client.CoreV1().ConfigMaps(identity.Namespace).Get(t.Context(), cmName, metav1.GetOptions{})
		assert.NoError(t, err)
		assert.Equal(t, testComputeNode, cm.Data[shadowNodeNameKey])
		assert.Equal(t, testSocketDir, cm.Data[shadowSocketDirKey])
	})

	t.Run("the same node and directory twice write once", func(t *testing.T) {
		client := newClient()
		p := tunnelProvider(client)

		p.publishShadowNode(t.Context(), pod, testComputeNode, testSocketDir)
		p.publishShadowNode(t.Context(), pod, testComputeNode, testSocketDir)

		assert.Len(t, configMapPatches(client), 1)
	})

	t.Run("a new socket directory on the same node is written", func(t *testing.T) {
		client := newClient()
		p := tunnelProvider(client)

		p.publishShadowNode(t.Context(), pod, testComputeNode, "")
		p.publishShadowNode(t.Context(), pod, testComputeNode, testSocketDir)

		assert.Len(t, configMapPatches(client), 2)
	})

	t.Run("an invalid socket directory publishes the node without it", func(t *testing.T) {
		client := newClient()
		p := tunnelProvider(client)

		p.publishShadowNode(t.Context(), pod, testComputeNode, "/shared/il/x;id")

		cm, err := client.CoreV1().ConfigMaps(identity.Namespace).Get(t.Context(), cmName, metav1.GetOptions{})
		assert.NoError(t, err)
		assert.Equal(t, testComputeNode, cm.Data[shadowNodeNameKey],
			"TCP forwards still work without a socket directory")
		assert.Equal(t, "", cm.Data[shadowSocketDirKey])
	})

	t.Run("an empty node name leaves the configmap alone while the job is queued", func(t *testing.T) {
		client := newClient()
		p := tunnelProvider(client)

		p.publishShadowNode(t.Context(), pod, "", "")

		assert.Empty(t, configMapPatches(client), "waiting for an allocation must not write to the API")
	})

	t.Run("repeated reports of the same node do not write again", func(t *testing.T) {
		client := newClient()
		p := tunnelProvider(client)

		p.publishShadowNode(t.Context(), pod, testComputeNode, "")
		before := len(configMapPatches(client))
		for range 5 {
			p.publishShadowNode(t.Context(), pod, testComputeNode, "")
		}

		assert.Len(t, configMapPatches(client), before,
			"the status loop polls continuously; only changes should reach the API")
	})

	t.Run("a pod with no shadow is skipped", func(t *testing.T) {
		client := newClient()
		p := &Provider{clientSet: client, config: Config{Network: Network{EnableTunnel: false}}}

		p.publishShadowNode(t.Context(), pod, testComputeNode, "")

		assert.Empty(t, configMapPatches(client))
	})

	// A deleted pod stays tracked while its remote deletion runs, after its cache entry
	// is forgotten: it must not write its node into its same-name successor's ConfigMap.
	t.Run("a configmap of a pod recreated under the same name is left alone", func(t *testing.T) {
		client := fake.NewSimpleClientset(&v1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: cmName, Namespace: identity.Namespace,
				Labels: map[string]string{shadowOwnerUIDLabel: "uid-successor"}},
			Data: map[string]string{shadowNodeNameKey: ""},
		})
		p := tunnelProvider(client)

		p.publishShadowNode(t.Context(), pod, testComputeNode, testSocketDir)

		assert.Empty(t, configMapPatches(client))
	})

	t.Run("the patch carries the resource version that was read", func(t *testing.T) {
		client := fake.NewSimpleClientset(&v1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: cmName, Namespace: identity.Namespace, ResourceVersion: "7"},
			Data:       map[string]string{shadowNodeNameKey: ""},
		})
		p := tunnelProvider(client)

		p.publishShadowNode(t.Context(), pod, testComputeNode, "")

		patches := configMapPatches(client)
		require.Len(t, patches, 1)
		assert.Contains(t, string(patches[0].(k8stesting.PatchAction).GetPatch()), `"resourceVersion":"7"`)
	})

	t.Run("forgetting a pod lets the same node be published again", func(t *testing.T) {
		client := newClient()
		p := tunnelProvider(client)

		p.publishShadowNode(t.Context(), pod, testComputeNode, "")
		p.forgetShadowNodeName(pod)
		p.publishShadowNode(t.Context(), pod, testComputeNode, "")

		assert.Len(t, configMapPatches(client), 2)
	})
}

func TestCleanupShadowResourcesRemovesNodeConfigMap(t *testing.T) {
	const name = testPodDefaultName
	ns := testNamespaceDefault

	client := fake.NewSimpleClientset(&v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: shadowNodeConfigMapName(name), Namespace: ns},
	})
	p := tunnelProvider(client)

	p.cleanupShadowResources(t.Context(), shadowResourceIdentity{Name: name, Namespace: ns})

	_, err := client.CoreV1().ConfigMaps(ns).Get(t.Context(), shadowNodeConfigMapName(name), metav1.GetOptions{})
	assert.True(t, apierrors.IsNotFound(err), "the node configmap should be deleted with the rest of the shadow")
}

// TestIsValidComputeNodeName pins what a plugin is allowed to report. The shadow
// puts this in front of ssh, so the interesting cases are the ones that would
// change the meaning of a command rather than merely look odd.
func TestIsValidComputeNodeName(t *testing.T) {
	valid := []string{
		"j18n3",
		"j18n3.liza.surf.nl",
		"hpc-cloud-test001.cern.ch",
		"as01r2b14",
		"10.0.0.7",
		"fe80::1",
		"node_7",
	}
	for _, name := range valid {
		assert.True(t, isValidComputeNodeName(name), "expected %q to be accepted", name)
	}

	hostile := map[string]string{
		"whitespace injects an ssh option": "h -oProxyCommand=touch",
		"tab does the same":                "h\t-oProxyCommand=touch",
		"newline":                          "h\nsecond",
		"quote breaks the relay command":   `x" ; touch /tmp/pwned ; echo "`,
		"command substitution":             "x$(id)y",
		"backticks":                        "x`id`y",
		"semicolon":                        "h;id",
		"glob":                             "h*",
		"leading hyphen reads as a flag":   "-oProxyCommand=touch",
		"empty":                            "",
	}
	for why, name := range hostile {
		assert.False(t, isValidComputeNodeName(name), "expected %q to be refused (%s)", name, why)
	}

	assert.False(t, isValidComputeNodeName(strings.Repeat("a", maxComputeNodeNameLen+1)),
		"a name longer than a DNS name is not one")
}

// TestIsValidSocketDir pins what a plugin may report as a socket directory. The
// shadow puts it in ssh arguments and in commands run on the node, and the longest
// socket path built from it must fit in a unix socket address.
func TestIsValidSocketDir(t *testing.T) {
	valid := []string{
		testSocketDir,
		"/a",
		"/scratch/user_1/il-sockets/0123456789ab",
		"/" + strings.Repeat("a", maxSocketDirLen-1),
	}
	for _, dir := range valid {
		assert.True(t, isValidSocketDir(dir), "expected %q to be accepted", dir)
	}

	refused := map[string]string{
		"relative path":              "shared/il/0123456789ab",
		"dot-dot segment":            "/shared/../etc",
		"dot segment":                "/shared/./il",
		"double slash":               "/shared//il",
		"trailing slash":             "/shared/il/",
		"space":                      "/shared/il dir",
		"dollar":                     "/shared/$HOME",
		"semicolon":                  "/shared/il;id",
		"quote":                      "/shared/il'x",
		"root alone":                 "/",
		"empty":                      "",
		"one byte over the limit":    "/" + strings.Repeat("a", maxSocketDirLen),
		"leading hyphen in segment":  "/-oProxyCommand=x",
		"newline":                    "/shared/il\nx",
		"longest socket would break": "/" + strings.Repeat("b", 120),
	}
	for why, dir := range refused {
		assert.False(t, isValidSocketDir(dir), "expected %q to be refused (%s)", dir, why)
	}
	assert.Len(t, "/"+strings.Repeat("a", maxSocketDirLen-1)+"/fwd-65535.sock", 107,
		"the longest socket path must fit the 108-byte sun_path with its NUL")
}

// TestPublishShadowNodeRefusesHostileNames makes sure a bad value never reaches
// the ConfigMap the shadow mounts, so neither forward mode can be made to run
// something other than ssh.
func TestPublishShadowNodeRefusesHostileNames(t *testing.T) {
	pod := podWithPort("nb", testNamespaceDefault, "uid-hostile")
	identity, err := computeShadowResourceIdentity(pod)
	assert.NoError(t, err)
	cmName := shadowNodeConfigMapName(identity.Name)

	for _, nodeName := range []string{"h -oProxyCommand=touch", `x" ; id ; echo "`, "x$(id)y"} {
		client := fake.NewSimpleClientset(&v1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: cmName, Namespace: identity.Namespace},
			Data:       map[string]string{shadowNodeNameKey: ""},
		})
		p := tunnelProvider(client)

		p.publishShadowNode(t.Context(), pod, nodeName, "")

		assert.Empty(t, configMapPatches(client), "must not patch for %q", nodeName)
		cm, err := client.CoreV1().ConfigMaps(identity.Namespace).Get(t.Context(), cmName, metav1.GetOptions{})
		assert.NoError(t, err)
		assert.Equal(t, "", cm.Data[shadowNodeNameKey])
	}
}

// endedPodWithShadow is an offloaded pod in the given phase together with the shadow
// Deployment and Service the VK rendered for it.
func endedPodWithShadow(t *testing.T, phase v1.PodPhase) (*v1.Pod, shadowResourceIdentity, *fake.Clientset) {
	t.Helper()
	pod := podWithPort("vllm", testNamespaceDefault, "uid-1")
	pod.Status.Phase = phase
	identity, err := computeShadowResourceIdentity(pod)
	require.NoError(t, err)
	meta := metav1.ObjectMeta{Name: identity.Name, Namespace: identity.Namespace}
	return pod, identity, fake.NewSimpleClientset(&appsv1.Deployment{ObjectMeta: meta}, &v1.Service{ObjectMeta: meta})
}

func deletes(client *fake.Clientset) int {
	n := 0
	for _, a := range client.Actions() {
		if a.GetVerb() == "delete" {
			n++
		}
	}
	return n
}

// A pod whose remote job ended stays in the API until someone deletes it, and so did
// its shadow: still Ready, still matching the pod's Services, and tunnelling to a
// compute node where nothing listens any more. Requests balanced onto it were reset.
func TestReleaseShadowOfEndedPod(t *testing.T) {
	for _, phase := range []v1.PodPhase{v1.PodFailed, v1.PodSucceeded} {
		t.Run(string(phase), func(t *testing.T) {
			pod, identity, client := endedPodWithShadow(t, phase)
			p := tunnelProvider(client)

			p.releaseShadowOfEndedPod(t.Context(), pod)

			_, err := client.AppsV1().Deployments(identity.Namespace).Get(t.Context(), identity.Name, metav1.GetOptions{})
			assert.True(t, apierrors.IsNotFound(err), "the shadow deployment of an ended pod should be deleted")
			_, err = client.CoreV1().Services(identity.Namespace).Get(t.Context(), identity.Name, metav1.GetOptions{})
			assert.True(t, apierrors.IsNotFound(err), "the shadow service of an ended pod should be deleted")
		})
	}
}

func TestReleaseShadowOfEndedPodOnlyOnce(t *testing.T) {
	pod, _, client := endedPodWithShadow(t, v1.PodFailed)
	p := tunnelProvider(client)

	p.releaseShadowOfEndedPod(t.Context(), pod)
	after := len(client.Actions())
	p.releaseShadowOfEndedPod(t.Context(), pod)

	assert.Equal(t, after, len(client.Actions()), "the status loop sees an ended pod every few seconds; it must not delete again")
}

func TestReleaseShadowKeepsTheShadowOfALivePod(t *testing.T) {
	for _, phase := range []v1.PodPhase{v1.PodPending, v1.PodRunning, ""} {
		t.Run(string(phase), func(t *testing.T) {
			pod, identity, client := endedPodWithShadow(t, phase)
			p := tunnelProvider(client)

			p.releaseShadowOfEndedPod(t.Context(), pod)

			assert.Zero(t, deletes(client))
			_, err := client.AppsV1().Deployments(identity.Namespace).Get(t.Context(), identity.Name, metav1.GetOptions{})
			assert.NoError(t, err)
		})
	}
}

func TestReleaseShadowIgnoresPodsWithoutAShadow(t *testing.T) {
	pod, _, client := endedPodWithShadow(t, v1.PodFailed)
	pod.Spec.Containers[0].Ports = nil
	p := tunnelProvider(client)

	p.releaseShadowOfEndedPod(t.Context(), pod)

	assert.Zero(t, deletes(client))
}

// TestCleanupKeepsShadowOfRecreatedPod covers a pod deleted and recreated under the same
// name: the new pod's shadow has the same names, and the old pod's cleanup can run after
// it was created. Only resources stamped with the old pod's UID may go.
func TestCleanupKeepsShadowOfRecreatedPod(t *testing.T) {
	const name, podName = testPodDefaultName, "nb-0"
	const oldUID, newUID = "uid-old", "uid-new"
	ns := testNamespaceDefault
	// Every pod is named podName: the successor (newUID) exists, the old pod is gone.
	cleanup := func(uid string) shadowResourceIdentity {
		return shadowResourceIdentity{Name: name, Namespace: ns, OwnerUID: uid, PodName: podName, PodNamespace: ns}
	}
	owned := func(objectName, uid string) metav1.ObjectMeta {
		meta := metav1.ObjectMeta{Name: objectName, Namespace: ns}
		stampShadowOwner(&meta, uid, ns+"/"+podName)
		return meta
	}
	withSuccessor := func(objects ...runtime.Object) *fake.Clientset {
		successor := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: podName, Namespace: ns, UID: k8stypes.UID(newUID)}}
		return fake.NewSimpleClientset(append(objects, successor)...)
	}
	stamped := func(uid string) *appsv1.Deployment {
		return &appsv1.Deployment{ObjectMeta: owned(name, uid)}
	}

	t.Run("the old pod's cleanup keeps the new pod's shadow", func(t *testing.T) {
		client := withSuccessor(stamped(newUID))
		p := tunnelProvider(client)

		p.cleanupShadowResources(t.Context(), cleanup(oldUID))

		_, err := client.AppsV1().Deployments(ns).Get(t.Context(), name, metav1.GetOptions{})
		assert.NoError(t, err)
	})

	t.Run("the owner's cleanup removes it", func(t *testing.T) {
		client := withSuccessor(stamped(newUID))
		p := tunnelProvider(client)

		p.cleanupShadowResources(t.Context(), cleanup(newUID))

		_, err := client.AppsV1().Deployments(ns).Get(t.Context(), name, metav1.GetOptions{})
		assert.True(t, apierrors.IsNotFound(err))
	})

	t.Run("an unstamped shadow from an older virtual kubelet is removed", func(t *testing.T) {
		client := withSuccessor(&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}})
		p := tunnelProvider(client)

		p.cleanupShadowResources(t.Context(), cleanup(oldUID))

		_, err := client.AppsV1().Deployments(ns).Get(t.Context(), name, metav1.GetOptions{})
		assert.True(t, apierrors.IsNotFound(err))
	})

	// cert-manager creates the TLS Secret without the owner label: it follows its Ingress.
	tlsObjects := func(owner string) []runtime.Object {
		return []runtime.Object{
			&networkingv1.Ingress{ObjectMeta: owned(name, owner)},
			&v1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name + "-tls", Namespace: ns}},
		}
	}
	for _, tc := range []struct {
		cleanup string
		kept    bool
	}{{cleanup: oldUID, kept: true}, {cleanup: newUID, kept: false}} {
		t.Run("the TLS secret follows its ingress, cleanup by "+tc.cleanup, func(t *testing.T) {
			client := withSuccessor(tlsObjects(newUID)...)
			p := tunnelProvider(client)
			p.config.Network.IngressTLS = true

			p.cleanupShadowResources(t.Context(), cleanup(tc.cleanup))

			_, err := client.CoreV1().Secrets(ns).Get(t.Context(), name+"-tls", metav1.GetOptions{})
			if tc.kept {
				assert.NoError(t, err)
			} else {
				assert.True(t, apierrors.IsNotFound(err))
			}
		})
	}

	// conflictOnce makes the first delete of resource fail with a conflict, after
	// changing the stored object with change, as an update landing between read and delete.
	conflictOnce := func(client *fake.Clientset, resource string, gvr schema.GroupVersionResource, change func(metav1.Object)) {
		first := true
		client.PrependReactor("delete", resource, func(k8stesting.Action) (bool, runtime.Object, error) {
			if !first {
				return false, nil, nil
			}
			first = false
			obj, err := client.Tracker().Get(gvr, ns, name)
			require.NoError(t, err)
			change(obj.(metav1.Object))
			require.NoError(t, client.Tracker().Update(gvr, obj, ns))
			return true, nil, apierrors.NewConflict(gvr.GroupResource(), name, errors.New("resourceVersion changed"))
		})
	}

	t.Run("an ingress taken over between read and delete keeps it and its TLS secret", func(t *testing.T) {
		client := withSuccessor(tlsObjects(oldUID)...)
		conflictOnce(client, "ingresses", networkingv1.SchemeGroupVersion.WithResource("ingresses"), func(o metav1.Object) {
			o.SetLabels(map[string]string{shadowOwnerUIDLabel: newUID})
		})
		p := tunnelProvider(client)
		p.config.Network.IngressTLS = true

		p.cleanupShadowResources(t.Context(), cleanup(oldUID))

		_, err := client.NetworkingV1().Ingresses(ns).Get(t.Context(), name, metav1.GetOptions{})
		assert.NoError(t, err)
		_, err = client.CoreV1().Secrets(ns).Get(t.Context(), name+"-tls", metav1.GetOptions{})
		assert.NoError(t, err)
	})

	t.Run("any other change between read and delete is retried", func(t *testing.T) {
		client := withSuccessor(stamped(oldUID))
		conflictOnce(client, "deployments", appsv1.SchemeGroupVersion.WithResource("deployments"), func(o metav1.Object) {
			o.SetAnnotations(map[string]string{"status": "updated"})
		})
		p := tunnelProvider(client)

		p.cleanupShadowResources(t.Context(), cleanup(oldUID))

		_, err := client.AppsV1().Deployments(ns).Get(t.Context(), name, metav1.GetOptions{})
		assert.True(t, apierrors.IsNotFound(err))
	})

	t.Run("the TLS secret stays when its ingress cannot be read", func(t *testing.T) {
		client := withSuccessor(tlsObjects(newUID)...)
		client.PrependReactor("get", "ingresses", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, apierrors.NewInternalError(errors.New("etcd timeout"))
		})
		p := tunnelProvider(client)
		p.config.Network.IngressTLS = true

		p.cleanupShadowResources(t.Context(), cleanup(oldUID))

		_, err := client.CoreV1().Secrets(ns).Get(t.Context(), name+"-tls", metav1.GetOptions{})
		assert.NoError(t, err)
	})

	t.Run("the delete is conditional on the resource version that was read", func(t *testing.T) {
		deployment := stamped(oldUID)
		deployment.UID, deployment.ResourceVersion = "deployment-uid", "41"
		client := withSuccessor(deployment)
		var preconditions *metav1.Preconditions
		client.PrependReactor("delete", "deployments", func(action k8stesting.Action) (bool, runtime.Object, error) {
			preconditions = action.(k8stesting.DeleteAction).GetDeleteOptions().Preconditions
			return false, nil, nil
		})
		p := tunnelProvider(client)

		p.cleanupShadowResources(t.Context(), cleanup(oldUID))

		require.NotNil(t, preconditions)
		require.NotNil(t, preconditions.UID)
		require.NotNil(t, preconditions.ResourceVersion)
		assert.Equal(t, k8stypes.UID("deployment-uid"), *preconditions.UID)
		assert.Equal(t, "41", *preconditions.ResourceVersion)
	})
}

// A resource stamped with another pod's UID is kept only while that pod is the one
// that has the name; the leftovers of a pod whose cleanup never ran are deleted.
func TestCleanupKeepsOnlyTheCurrentPodsResources(t *testing.T) {
	const name, podName, staleUID, currentUID = testPodDefaultName, "nb-0", "uid-stale", "uid-live"
	ns := testNamespaceDefault
	identity := shadowResourceIdentity{Name: name, Namespace: ns, OwnerUID: "uid-cleaning", PodName: podName, PodNamespace: ns}
	stampedService := func(owner, ownerPod string) *v1.Service {
		meta := metav1.ObjectMeta{Name: name, Namespace: ns}
		stampShadowOwner(&meta, owner, ownerPod)
		return &v1.Service{ObjectMeta: meta}
	}
	podIn := func(namespace, podName, uid string) *v1.Pod {
		return &v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: podName, Namespace: namespace, UID: k8stypes.UID(uid)}}
	}
	pod := func(podName, uid string) *v1.Pod { return podIn(ns, podName, uid) }
	serviceLeft := func(t *testing.T, client *fake.Clientset) bool {
		t.Helper()
		tunnelProvider(client).cleanupShadowResources(t.Context(), identity)
		_, err := client.CoreV1().Services(ns).Get(t.Context(), name, metav1.GetOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			t.Fatal(err)
		}
		return err == nil
	}

	t.Run("kept for the pod that now has the name", func(t *testing.T) {
		client := fake.NewSimpleClientset(stampedService(currentUID, ns+"/"+podName), pod(podName, currentUID))
		assert.True(t, serviceLeft(t, client))
	})

	t.Run("deleted when another pod now has the name", func(t *testing.T) {
		client := fake.NewSimpleClientset(stampedService(staleUID, ns+"/"+podName), pod(podName, currentUID))
		assert.False(t, serviceLeft(t, client), "a leftover of a pod that is gone stays forever otherwise")
	})

	t.Run("deleted when no pod has the name", func(t *testing.T) {
		client := fake.NewSimpleClientset(stampedService(staleUID, ns+"/"+podName))
		assert.False(t, serviceLeft(t, client))
	})

	t.Run("kept when the pod cannot be read", func(t *testing.T) {
		client := fake.NewSimpleClientset(stampedService(staleUID, ns+"/"+podName))
		client.PrependReactor("get", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, apierrors.NewInternalError(errors.New("etcd timeout"))
		})
		assert.True(t, serviceLeft(t, client))
	})

	// Stamped before the annotation existed: the owner is looked up under the
	// cleaning pod's name.
	t.Run("without the annotation, deleted when another pod now has the name", func(t *testing.T) {
		client := fake.NewSimpleClientset(stampedService(staleUID, ""), pod(podName, currentUID))
		assert.False(t, serviceLeft(t, client))
	})

	// Namespaces a--b and a-b get the same shadow names too.
	t.Run("kept for a live pod of another namespace that shares the shadow name", func(t *testing.T) {
		const otherNamespace, otherUID = "other-ns", "uid-elsewhere"
		client := fake.NewSimpleClientset(stampedService(otherUID, otherNamespace+"/"+podName),
			pod(podName, currentUID), podIn(otherNamespace, podName, otherUID))
		assert.True(t, serviceLeft(t, client))
	})

	// web.0 and web-0 get the same shadow name: deleting one must not take the
	// shadow the other one, still running, took over.
	t.Run("kept for a live pod of another name that shares the shadow name", func(t *testing.T) {
		const other, otherUID = "nb.0", "uid-other"
		client := fake.NewSimpleClientset(stampedService(otherUID, ns+"/"+other), pod(podName, currentUID), pod(other, otherUID))
		assert.True(t, serviceLeft(t, client))
	})
}

// The cleanup of a deleted pod and the shadow of its same-name successor must not
// interleave: the new Ingress could otherwise appear between the old Ingress and the
// TLS Secret being deleted, and lose the Secret.
func TestShadowCleanupAndApplyAreSerialised(t *testing.T) {
	const name = testPodDefaultName
	ns := testNamespaceDefault
	waitsForLock := func(t *testing.T, p *Provider, run func()) {
		t.Helper()
		unlock := p.lockShadow(ns, name)
		done := make(chan struct{})
		go func() { run(); close(done) }()
		select {
		case <-done:
			unlock()
			t.Fatal("ran while the shadow's lock was held")
		case <-time.After(100 * time.Millisecond):
		}
		unlock()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("did not run once the lock was released")
		}
	}

	t.Run("cleanup", func(t *testing.T) {
		client := fake.NewSimpleClientset()
		p := tunnelProvider(client)
		waitsForLock(t, p, func() {
			p.cleanupShadowResources(t.Context(), shadowResourceIdentity{Name: name, Namespace: ns})
		})
	})

	t.Run("apply", func(t *testing.T) {
		client := fake.NewSimpleClientset()
		p := tunnelProvider(client)
		manifest := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: " + name + "\n  namespace: " + ns + "\n"
		waitsForLock(t, p, func() {
			_, _, err := p.applyShadowObjects(t.Context(), manifest, &ShadowTemplateData{Name: name, Namespace: ns})
			assert.NoError(t, err)
		})
		_, err := client.CoreV1().ConfigMaps(ns).Get(t.Context(), name, metav1.GetOptions{})
		assert.NoError(t, err)
	})
}

// Applying a shadow stamps every object with the pod's UID, and an object left by a
// deleted pod with the same name is updated in place with the new pod's UID.
func TestApplyShadowStampsOwner(t *testing.T) {
	const name, previousOwner, owner = testPodDefaultName, "uid-before", "uid-after"
	ns := testNamespaceDefault
	client := fake.NewSimpleClientset(&v1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
		Name: name, Namespace: ns, Labels: map[string]string{shadowOwnerUIDLabel: previousOwner},
	}})
	p := tunnelProvider(client)
	manifest := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: " + name + "\n  namespace: " + ns + "\n" +
		"---\napiVersion: v1\nkind: Service\nmetadata:\n  name: " + name + "\n  namespace: " + ns + "\n" +
		"spec:\n  ports:\n  - port: 80\n"

	_, _, err := p.applyShadowObjects(t.Context(), manifest, &ShadowTemplateData{Name: name, Namespace: ns, OwnerUID: owner, OwnerPod: ns + "/nb-0"})
	require.NoError(t, err)

	cm, err := client.CoreV1().ConfigMaps(ns).Get(t.Context(), name, metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, owner, cm.Labels[shadowOwnerUIDLabel], "the previous pod's object is relabelled")
	assert.Equal(t, ns+"/nb-0", cm.Annotations[shadowOwnerPodAnnotation])
	svc, err := client.CoreV1().Services(ns).Get(t.Context(), name, metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, owner, svc.Labels[shadowOwnerUIDLabel])
	assert.Equal(t, ns+"/nb-0", svc.Annotations[shadowOwnerPodAnnotation])
}

func TestShadowIdentityAndNodeConfigMapCarryOwner(t *testing.T) {
	pod := podWithPort("nb", testNamespaceDefault, "uid-7")
	identity, err := computeShadowResourceIdentity(pod)
	require.NoError(t, err)
	assert.Equal(t, "uid-7", identity.OwnerUID)

	client := fake.NewSimpleClientset()
	p := tunnelProvider(client)
	require.NoError(t, p.resetShadowNodeConfigMap(t.Context(), identity))
	cm, err := client.CoreV1().ConfigMaps(identity.Namespace).Get(t.Context(), shadowNodeConfigMapName(identity.Name), metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "uid-7", cm.Labels[shadowOwnerUIDLabel])
	assert.Equal(t, testNamespaceDefault+"/nb", cm.Annotations[shadowOwnerPodAnnotation])
}
