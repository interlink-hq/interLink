package virtualkubelet

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestRatholeBackend_ClientCommandWebsocket(t *testing.T) {
	backend := &RatholeBackend{
		cfg: Network{
			TunnelType:  tunnelTypeRathole,
			WildcardDNS: "tunnel.example.com",
		},
	}
	backend.bindProvider(&Provider{clientSet: fake.NewClientset()})

	td := WstunnelTemplateData{
		Name:           "mypod-default",
		Namespace:      "default-wstunnel",
		WildcardDNS:    "tunnel.example.com",
		RandomPassword: "token123",
		ExposedPorts: []PortMapping{
			{Port: 8080, Protocol: "TCP"},
		},
	}

	cmd, err := backend.ClientCommand(context.Background(), td, &v1.Pod{})
	require.NoError(t, err)
	assert.Contains(t, cmd, DefaultRatholeExecutableURL)
	assert.Contains(t, cmd, "rathole-client.toml")
	assert.Equal(t, annRatholeClientCmds, backend.ClientAnnotationKey())
}

func TestRatholeBackend_KubernetesTemplate(t *testing.T) {
	backend := &RatholeBackend{cfg: Network{TunnelType: tunnelTypeRathole}}
	tpl, err := backend.KubernetesTemplate()
	require.NoError(t, err)
	assert.Contains(t, tpl, "rapiz1/rathole")
}
