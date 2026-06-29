package virtualkubelet

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func boolPtr(v bool) *bool {
	return &v
}

func readyPod(name, namespace, ownerName, podIP string) *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			OwnerReferences: []metav1.OwnerReference{{
				Kind:       "ReplicaSet",
				Name:       ownerName,
				Controller: boolPtr(true),
			}},
		},
		Status: v1.PodStatus{
			Phase: v1.PodRunning,
			PodIP: podIP,
			Conditions: []v1.PodCondition{{
				Type:   v1.PodReady,
				Status: v1.ConditionTrue,
			}},
		},
	}
}

func deploymentReplicaSet(name, namespace, deploymentName, revision string) *appsv1.ReplicaSet {
	return &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{"app": "demo"},
			Annotations: map[string]string{
				"deployment.kubernetes.io/revision": revision,
			},
			OwnerReferences: []metav1.OwnerReference{{
				Kind:       "Deployment",
				Name:       deploymentName,
				Controller: boolPtr(true),
			}},
		},
	}
}

func TestComputeWstunnelResourceNamesForSameNamespaceKeepsNamespace(t *testing.T) {
	name, namespace := computeWstunnelResourceNamesForSameNamespace("DemoPod", "team-a")

	assert.Equal(t, "team-a", namespace)
	assert.Equal(t, "wstunnel-demopod-team-a", name)
}

func TestExecuteWstunnelTemplateUsesExpectedEmbeddedTemplate(t *testing.T) {
	t.Run("standard template aligns ingress host and tls", func(t *testing.T) {
		p := &Provider{}
		data := WstunnelTemplateData{
			Name:                 "demo-team-a",
			Namespace:            "team-a-wstunnel",
			RandomPassword:       "secret",
			FullMesh:             false,
			IngressHost:          "demo-team-a-team-a-wstunnel.tunnel.example.com",
			IngressTLS:           true,
			IngressTLSSecretName: "demo-team-a-tls",
			IngressClusterIssuer: "lets-issuer",
			ExposedPorts:         []PortMapping{{Port: 8080, Name: "http", Protocol: "TCP"}},
		}

		manifest, err := p.executeWstunnelTemplate(context.Background(), data)
		require.NoError(t, err)
		assert.Contains(t, manifest, "host: demo-team-a-team-a-wstunnel.tunnel.example.com")
		assert.NotContains(t, manifest, "host: ws-demo-team-a")
		assert.Contains(t, manifest, "cert-manager.io/cluster-issuer: \"lets-issuer\"")
		assert.Contains(t, manifest, "secretName: demo-team-a-tls")
		assert.NotContains(t, manifest, "kind: ConfigMap")
	})

	t.Run("full mesh selects wireguard template", func(t *testing.T) {
		p := &Provider{}
		data := WstunnelTemplateData{
			Name:                 "demo-team-a",
			Namespace:            "team-a-wstunnel",
			RandomPassword:       "secret",
			FullMesh:             true,
			IngressHost:          "demo-team-a-team-a-wstunnel.tunnel.example.com",
			IngressTLS:           true,
			IngressTLSSecretName: "demo-team-a-tls",
			IngressClusterIssuer: "lets-issuer",
			WGPrivateKey:         "server-private",
			ClientPublicKey:      "client-public",
			ExposedPorts:         []PortMapping{{Port: 8080, Name: "http", Protocol: "TCP"}},
		}

		manifest, err := p.executeWstunnelTemplate(context.Background(), data)
		require.NoError(t, err)
		assert.Contains(t, manifest, "kind: ConfigMap")
		assert.Contains(t, manifest, "number: 28080")
		assert.Contains(t, manifest, "host: demo-team-a-team-a-wstunnel.tunnel.example.com")
	})
}

func TestAddWstunnelClientAnnotationUsesTLSURLAndReturnsPatchErrors(t *testing.T) {
	t.Run("uses wss endpoint when tls is enabled", func(t *testing.T) {
		pod := &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "demo",
				Namespace: "team-a",
			},
		}
		client := fake.NewSimpleClientset(pod.DeepCopy())
		p := &Provider{clientSet: client}
		td := &WstunnelTemplateData{
			Name:                "demo-team-a",
			Namespace:           "team-a-wstunnel",
			RandomPassword:      "secret",
			IngressHost:         "demo-team-a-team-a-wstunnel.tunnel.example.com",
			IngressWebsocketURL: "wss://demo-team-a-team-a-wstunnel.tunnel.example.com:443",
			IngressTLS:          true,
			ExposedPorts:        []PortMapping{{Port: 8080, Name: "http", Protocol: "TCP"}},
		}

		err := p.addWstunnelClientAnnotation(context.Background(), pod, td)
		require.NoError(t, err)

		updated, getErr := client.CoreV1().Pods("team-a").Get(context.Background(), "demo", metav1.GetOptions{})
		require.NoError(t, getErr)
		assert.Contains(t, updated.Annotations[annWSTunnelClientCmds], "wss://demo-team-a-team-a-wstunnel.tunnel.example.com:443")
	})

	t.Run("returns patch errors", func(t *testing.T) {
		p := &Provider{clientSet: fake.NewSimpleClientset()}
		pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "missing", Namespace: "team-a"}}
		td := &WstunnelTemplateData{Name: "demo", Namespace: "team-a-wstunnel", RandomPassword: "secret"}

		err := p.addWstunnelClientAnnotation(context.Background(), pod, td)
		require.Error(t, err)
	})
}

func TestSetupWireGuardConfigReusesPersistedKeys(t *testing.T) {
	p := &Provider{}
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "demo",
			Namespace:   "team-a",
			Annotations: map[string]string{},
		},
	}

	first := &WstunnelTemplateData{}
	second := &WstunnelTemplateData{}

	require.NoError(t, p.setupWireGuardConfig(context.Background(), pod, first))
	require.NoError(t, p.setupWireGuardConfig(context.Background(), pod, second))

	assert.Equal(t, first.WGPrivateKey, second.WGPrivateKey)
	assert.Equal(t, first.ClientPrivateKey, second.ClientPrivateKey)
	assert.Equal(t, first.ClientPublicKey, second.ClientPublicKey)
	assert.Equal(t, first.ClientPrivateKey, pod.Annotations[annWGClientPrivateKey])
}

func TestPrependAnnotationScriptIsIdempotent(t *testing.T) {
	script := "echo preparing\n"
	combined := prependAnnotationScript("echo existing\n", script)
	assert.Equal(t, combined, prependAnnotationScript(combined, script))
	assert.Equal(t, 1, strings.Count(prependAnnotationScript(combined, script), script))
}

func TestApplyWstunnelManifestsDecodeFailureCleansUpResources(t *testing.T) {
	p := &Provider{clientSet: fake.NewSimpleClientset()}
	manifest := `
apiVersion: v1
kind: Service
metadata:
  name: demo
  namespace: team-a-wstunnel
spec:
  selector:
    app: demo
  ports:
  - port: 8080
    targetPort: 8080
---
:
`

	_, err := p.applyWstunnelManifests(context.Background(), manifest, &WstunnelTemplateData{Namespace: "team-a-wstunnel"})
	require.Error(t, err)

	_, getErr := p.clientSet.CoreV1().Services("team-a-wstunnel").Get(context.Background(), "demo", metav1.GetOptions{})
	require.Error(t, getErr)
}

func TestGetCurrentDeploymentPodPrefersLatestReplicaSet(t *testing.T) {
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "demo",
			Namespace: "team-a-wstunnel",
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "demo"},
			},
		},
	}
	oldRS := deploymentReplicaSet("demo-old", "team-a-wstunnel", "demo", "1")
	newRS := deploymentReplicaSet("demo-new", "team-a-wstunnel", "demo", "2")
	oldPod := readyPod("demo-old-pod", "team-a-wstunnel", "demo-old", "10.0.0.10")
	oldPod.Labels = map[string]string{"app": "demo"}
	newPod := readyPod("demo-new-pod", "team-a-wstunnel", "demo-new", "10.0.0.11")
	newPod.Labels = map[string]string{"app": "demo"}

	p := &Provider{clientSet: fake.NewSimpleClientset(deployment, oldRS, newRS, oldPod, newPod)}

	pod, err := p.getCurrentDeploymentPod(context.Background(), deployment)
	require.NoError(t, err)
	require.NotNil(t, pod)
	assert.Equal(t, "demo-new-pod", pod.Name)
}

func TestIsPodReadyRequiresRunningReadyPodWithIP(t *testing.T) {
	tests := []struct {
		name string
		pod  *v1.Pod
		want bool
	}{
		{
			name: "ready running pod with ip",
			pod:  readyPod("demo", "ns", "rs", "10.0.0.2"),
			want: true,
		},
		{
			name: "missing ip",
			pod: func() *v1.Pod {
				pod := readyPod("demo", "ns", "rs", "")
				return pod
			}(),
			want: false,
		},
		{
			name: "not ready condition",
			pod: func() *v1.Pod {
				pod := readyPod("demo", "ns", "rs", "10.0.0.2")
				pod.Status.Conditions[0].Status = v1.ConditionFalse
				return pod
			}(),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isPodReady(tt.pod))
		})
	}
}
