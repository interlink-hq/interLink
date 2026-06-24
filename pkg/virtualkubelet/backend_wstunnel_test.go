package virtualkubelet

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWstunnelBackend_ClientCommand(t *testing.T) {
	backend := &WstunnelBackend{cfg: Network{}}
	td := WstunnelTemplateData{
		Name:           "mypod-default",
		Namespace:      "default-wstunnel",
		WildcardDNS:    "tunnel.example.com",
		RandomPassword: "token123",
		ExposedPorts: []PortMapping{
			{Port: 8080, Protocol: "TCP"},
			{Port: 5353, Protocol: "UDP"},
		},
	}

	cmd, err := backend.ClientCommand(context.Background(), td, nil)
	assert.NoError(t, err)
	assert.Contains(t, cmd, "-R tcp://0.0.0.0:8080:localhost:8080")
	assert.NotContains(t, cmd, "5353")
	assert.Equal(t, annWSTunnelClientCmds, backend.ClientAnnotationKey())
}
