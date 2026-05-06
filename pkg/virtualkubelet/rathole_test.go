package virtualkubelet

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// TestRatholeTemplateExecution verifies that the built-in rathole template can be
// loaded and executed without errors when TunnelType is "rathole".
func TestRatholeTemplateExecution(t *testing.T) {
	p := &Provider{
		config: Config{
			Network: Network{
				TunnelType:  "rathole",
				WildcardDNS: "tunnel.example.com",
			},
		},
		clientSet: fake.NewClientset(),
	}

	data := WstunnelTemplateData{
		Name:           "my-pod-default",
		Namespace:      "default-wstunnel",
		RandomPassword: "abc123",
		WildcardDNS:    "tunnel.example.com",
		ExposedPorts: []PortMapping{
			{Port: 8080, Name: "http", Protocol: "TCP"},
			{Port: 9090, Name: "metrics", Protocol: "TCP"},
		},
	}

	ctx := context.Background()
	yaml, err := p.executeWstunnelTemplate(ctx, data)
	require.NoError(t, err)
	assert.NotEmpty(t, yaml)

	// Verify the rendered YAML contains rathole-specific markers
	assert.Contains(t, yaml, "rathole-config", "ConfigMap name should reference rathole")
	assert.Contains(t, yaml, "rapiz1/rathole", "should use the default rathole image")
	assert.Contains(t, yaml, "bind_addr = \"0.0.0.0:2333\"", "server control port")
	assert.Contains(t, yaml, "token = \"abc123\"", "token from RandomPassword")
	assert.Contains(t, yaml, "bind_addr = \"0.0.0.0:8080\"", "port 8080 should be forwarded")
	assert.Contains(t, yaml, "bind_addr = \"0.0.0.0:9090\"", "port 9090 should be forwarded")
	assert.Contains(t, yaml, "rathole-my-pod-default.tunnel.example.com", "Ingress host should use rathole prefix")
}

// TestWstunnelTemplateUnchanged verifies that the existing wstunnel template is still
// selected when TunnelType is empty (backward-compatible default).
func TestWstunnelTemplateUnchanged(t *testing.T) {
	p := &Provider{
		config: Config{
			Network: Network{
				// TunnelType deliberately empty → wstunnel
				WildcardDNS: "tunnel.example.com",
			},
		},
		clientSet: fake.NewClientset(),
	}

	data := WstunnelTemplateData{
		Name:           "my-pod-default",
		Namespace:      "default-wstunnel",
		RandomPassword: "abc123",
		WildcardDNS:    "tunnel.example.com",
		ExposedPorts: []PortMapping{
			{Port: 8080, Name: "http", Protocol: "TCP"},
		},
	}

	ctx := context.Background()
	yaml, err := p.executeWstunnelTemplate(ctx, data)
	require.NoError(t, err)
	assert.NotEmpty(t, yaml)

	// The default wstunnel template should not contain rathole markers
	assert.NotContains(t, yaml, "rathole-config")
	assert.Contains(t, yaml, "wstunnel", "should use wstunnel image/command")
}

// TestRatholeClientAnnotation verifies that addWstunnelClientAnnotation sets
// the rathole annotation and removes any stale wstunnel annotation.
func TestRatholeClientAnnotation(t *testing.T) {
	fakeClient := fake.NewClientset()

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
			Annotations: map[string]string{
				// Simulate a stale wstunnel annotation from a previous run
				annWSTunnelClientCmds: "old-wstunnel-cmd",
			},
		},
	}
	// Create the pod in the fake client so Patch succeeds
	_, err := fakeClient.CoreV1().Pods(pod.Namespace).Create(context.Background(), pod, metav1.CreateOptions{})
	require.NoError(t, err)

	p := &Provider{
		config: Config{
			Network: Network{
				TunnelType:  "rathole",
				WildcardDNS: "tunnel.example.com",
			},
		},
		clientSet: fakeClient,
	}

	td := &WstunnelTemplateData{
		Name:           "my-pod-default",
		Namespace:      "default-wstunnel",
		RandomPassword: "secrettoken",
		WildcardDNS:    "tunnel.example.com",
		ExposedPorts: []PortMapping{
			{Port: 8080, Name: "http", Protocol: "TCP"},
		},
	}

	err = p.addWstunnelClientAnnotation(context.Background(), pod, td)
	require.NoError(t, err)

	// The rathole annotation should be set
	ratholeCmd, ok := pod.Annotations[annRatholeClientCmds]
	assert.True(t, ok, "rathole client command annotation should be present")
	assert.NotEmpty(t, ratholeCmd)
	assert.Contains(t, ratholeCmd, DefaultRatholeExecutableURL, "should embed the default rathole URL")
	// The base64-encoded client config should be included
	assert.True(t, strings.Contains(ratholeCmd, "base64"), "command should decode a base64 client config")

	// The stale wstunnel annotation should be removed
	_, wstunnelPresent := pod.Annotations[annWSTunnelClientCmds]
	assert.False(t, wstunnelPresent, "stale wstunnel annotation should be cleared in rathole mode")
}

// TestRatholeClientAnnotationCustomCommand verifies that a custom RatholeCommand template
// is honoured when set.
func TestRatholeClientAnnotationCustomCommand(t *testing.T) {
	fakeClient := fake.NewClientset()

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "test-pod",
			Namespace:   "default",
			Annotations: map[string]string{},
		},
	}
	_, err := fakeClient.CoreV1().Pods(pod.Namespace).Create(context.Background(), pod, metav1.CreateOptions{})
	require.NoError(t, err)

	customCmd := "my-custom-rathole-installer %s && my-custom-start %s &"
	p := &Provider{
		config: Config{
			Network: Network{
				TunnelType:     "rathole",
				WildcardDNS:    "tunnel.example.com",
				RatholeCommand: customCmd,
			},
		},
		clientSet: fakeClient,
	}

	td := &WstunnelTemplateData{
		Name:           "my-pod-default",
		Namespace:      "default-wstunnel",
		RandomPassword: "token",
		WildcardDNS:    "tunnel.example.com",
		ExposedPorts: []PortMapping{
			{Port: 8080, Name: "http", Protocol: "TCP"},
		},
	}

	err = p.addWstunnelClientAnnotation(context.Background(), pod, td)
	require.NoError(t, err)

	ratholeCmd, ok := pod.Annotations[annRatholeClientCmds]
	assert.True(t, ok)
	assert.Contains(t, ratholeCmd, "my-custom-rathole-installer", "custom command template should be used")
}
