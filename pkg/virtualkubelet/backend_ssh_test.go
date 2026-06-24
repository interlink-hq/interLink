package virtualkubelet

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestSSHBackend_ClientCommand(t *testing.T) {
	fakeClient := fake.NewClientset(
		&v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "jump-key",
				Namespace: "interlink",
			},
			Data: map[string][]byte{
				"id_ed25519": []byte("private-key-material"),
			},
		},
	)

	backend := &SSHBackend{
		cfg: Network{
			TunnelType:                tunnelTypeSSH,
			SSHJumpHost:               "user@jump.example.com:22",
			SSHJumpKeySecretName:      "jump-key",
			SSHJumpKeySecretNamespace: "interlink",
		},
	}
	backend.bindProvider(&Provider{
		clientSet: fakeClient,
		config:    Config{Namespace: "interlink"},
	})

	td := WstunnelTemplateData{
		ExposedPorts: []PortMapping{
			{Port: 8080, Protocol: "TCP"},
			{Port: 53, Protocol: "UDP"},
		},
	}

	cmd, err := backend.ClientCommand(context.Background(), td, &v1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "default"}})
	require.NoError(t, err)
	assert.Contains(t, cmd, "ssh -i /tmp/ssh_jump_key")
	assert.Contains(t, cmd, "-J user@jump.example.com:22")
	assert.Contains(t, cmd, "-L 0.0.0.0:8080:localhost:8080")
	assert.Contains(t, cmd, " localhost &")
	assert.NotContains(t, cmd, ":53:")
	assert.Equal(t, annSSHClientCmds, backend.ClientAnnotationKey())
}

func TestSSHBackend_CustomCommandVerbValidation(t *testing.T) {
	fakeClient := fake.NewClientset(
		&v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "jump-key",
				Namespace: "interlink",
			},
			Data: map[string][]byte{
				"id_rsa": []byte("private-key-material"),
			},
		},
	)

	backend := &SSHBackend{
		cfg: Network{
			TunnelType:                tunnelTypeSSH,
			SSHJumpHost:               "user@jump.example.com:22",
			SSHJumpKeySecretName:      "jump-key",
			SSHJumpKeySecretNamespace: "interlink",
			SSHCommand:                "custom %s %s %s",
		},
	}
	backend.bindProvider(&Provider{
		clientSet: fakeClient,
		config:    Config{Namespace: "interlink"},
	})

	_, err := backend.ClientCommand(context.Background(), WstunnelTemplateData{
		ExposedPorts: []PortMapping{
			{Port: 8080, Protocol: "TCP"},
			{Port: 9090, Protocol: "TCP"},
		},
	}, &v1.Pod{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must have exactly")
}
