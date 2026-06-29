package virtualkubelet

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestIsMeshNetworkingDisabled(t *testing.T) {
	t.Run("nil pod", func(t *testing.T) {
		assert.False(t, isMeshNetworkingDisabled(nil))
	})

	tests := []struct {
		name        string
		annotations map[string]string
		expected    bool
	}{
		{
			name:     "missing annotation",
			expected: false,
		},
		{
			name:        "disabled",
			annotations: map[string]string{annMeshNetworkDisabled: "disabled"},
			expected:    true,
		},
		{
			name:        "disabled case insensitive",
			annotations: map[string]string{annMeshNetworkDisabled: " DISABLED "},
			expected:    true,
		},
		{
			name:        "not disabled",
			annotations: map[string]string{annMeshNetworkDisabled: "enabled"},
			expected:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pod := &v1.Pod{}
			if tt.annotations != nil {
				pod.Annotations = tt.annotations
			}
			assert.Equal(t, tt.expected, isMeshNetworkingDisabled(pod))
		})
	}
}

func TestExecuteWstunnelTemplateIngressTLS(t *testing.T) {
	p := &Provider{}
	manifest, err := p.executeWstunnelTemplate(t.Context(), WstunnelTemplateData{
		Name:                 "pod-default",
		Namespace:            "default-wstunnel",
		RandomPassword:       "path-prefix",
		WildcardDNS:          "tunnel.example.com",
		IngressTLS:           true,
		IngressClusterIssuer: "lets-issuer",
	})

	assert.NoError(t, err)
	assert.Contains(t, manifest, "cert-manager.io/cluster-issuer: lets-issuer")
	assert.Contains(t, manifest, "- pod-default-default-wstunnel.tunnel.example.com")
	assert.Contains(t, manifest, "host: pod-default-default-wstunnel.tunnel.example.com")
	assert.NotContains(t, manifest, "host: ws-pod-default.tunnel.example.com")
	assert.Equal(t, 1, strings.Count(manifest, "secretName: pod-default-tls"))
}

func TestExecuteWstunnelTemplateFullMeshSelectsWireGuardTemplate(t *testing.T) {
	p := &Provider{}
	manifest, err := p.executeWstunnelTemplate(t.Context(), WstunnelTemplateData{
		Name:            "pod-default",
		Namespace:       "default-wstunnel",
		RandomPassword:  "path-prefix",
		WildcardDNS:     "tunnel.example.com",
		FullMesh:        true,
		WGPrivateKey:    "server-private-key",
		ClientPublicKey: "client-public-key",
	})

	assert.NoError(t, err)
	assert.Contains(t, manifest, "name: pod-default-wg-config")
	assert.Contains(t, manifest, "name: port-forwarder")
	assert.Contains(t, manifest, "number: 28080")
}

func TestComputeWstunnelResourceIdentityUsesFinalNamespace(t *testing.T) {
	t.Run("default shadow namespace", func(t *testing.T) {
		identity, err := computeWstunnelResourceIdentity(&v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-pod",
				Namespace: "default",
			},
		})

		assert.NoError(t, err)
		assert.Equal(t, "my-pod-default", identity.Name)
		assert.Equal(t, "default-wstunnel", identity.Namespace)
	})

	t.Run("same namespace keeps original namespace", func(t *testing.T) {
		identity, err := computeWstunnelResourceIdentity(&v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-pod",
				Namespace: "default",
				Annotations: map[string]string{
					"interlink.eu/shadow-same-ns": "true",
				},
			},
		})

		assert.NoError(t, err)
		assert.Equal(t, "wstunnel-my-pod-default", identity.Name)
		assert.Equal(t, "default", identity.Namespace)
	})
}

func TestShouldCreateWstunnel(t *testing.T) {
	basePod := &v1.Pod{
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name: "main",
					Ports: []v1.ContainerPort{
						{ContainerPort: 8080},
					},
				},
			},
		},
	}

	tests := []struct {
		name     string
		network  Network
		pod      *v1.Pod
		expected bool
	}{
		{
			name:     "disabled tunnel",
			network:  Network{EnableTunnel: false},
			pod:      basePod.DeepCopy(),
			expected: false,
		},
		{
			name:     "enabled tunnel with exposed port",
			network:  Network{EnableTunnel: true},
			pod:      basePod.DeepCopy(),
			expected: true,
		},
		{
			name:    "enabled tunnel with extra ports annotation",
			network: Network{EnableTunnel: true},
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"interlink.eu/wstunnel-extra-ports": "9090",
					},
				},
			},
			expected: true,
		},
		{
			name:    "pod vpn annotation disables wstunnel",
			network: Network{EnableTunnel: true},
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"interlink.eu/pod-vpn": "true",
					},
				},
				Spec: basePod.Spec,
			},
			expected: false,
		},
		{
			name:    "mesh disabled annotation still allows port-forward tunnel",
			network: Network{EnableTunnel: true, FullMesh: true},
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						annMeshNetworkDisabled: "disabled",
					},
				},
				Spec: basePod.Spec,
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Provider{
				config: Config{
					Network: tt.network,
				},
			}
			assert.Equal(t, tt.expected, p.shouldCreateWstunnel(tt.pod))
		})
	}
}
