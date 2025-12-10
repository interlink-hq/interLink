package virtualkubelet

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConfig_DefaultValues(t *testing.T) {
	config := Config{}

	assert.Empty(t, config.InterlinkURL)
	assert.Empty(t, config.InterlinkPort)
	assert.False(t, config.VerboseLogging)
	assert.False(t, config.ErrorsOnlyLogging)
	assert.False(t, config.DisableProjectedVolumes)
	assert.False(t, config.DisableCSR)
}

func TestTLSConfig_Structure(t *testing.T) {
	tlsConfig := TLSConfig{
		Enabled:    true,
		CertFile:   "/path/to/cert.pem",
		KeyFile:    "/path/to/key.pem",
		CACertFile: "/path/to/ca.pem",
	}

	assert.True(t, tlsConfig.Enabled)
	assert.Equal(t, "/path/to/cert.pem", tlsConfig.CertFile)
	assert.Equal(t, "/path/to/key.pem", tlsConfig.KeyFile)
	assert.Equal(t, "/path/to/ca.pem", tlsConfig.CACertFile)
}

func TestResources_Configuration(t *testing.T) {
	resources := Resources{
		CPU:    "100",
		Memory: "128Gi",
		Pods:   "100",
		Accelerators: []Accelerator{
			{
				ResourceType: "nvidia.com/gpu",
				Model:        "A100",
				Available:    8,
			},
		},
	}

	assert.Equal(t, "100", resources.CPU)
	assert.Equal(t, "128Gi", resources.Memory)
	assert.Equal(t, "100", resources.Pods)
	assert.Len(t, resources.Accelerators, 1)
	assert.Equal(t, "nvidia.com/gpu", resources.Accelerators[0].ResourceType)
	assert.Equal(t, 8, resources.Accelerators[0].Available)
}

func TestTaintSpec_Configuration(t *testing.T) {
	taint := TaintSpec{
		Key:    "virtual-node",
		Value:  "interlink",
		Effect: "NoSchedule",
	}

	assert.Equal(t, "virtual-node", taint.Key)
	assert.Equal(t, "interlink", taint.Value)
	assert.Equal(t, "NoSchedule", taint.Effect)
}

func TestPodCIDR_Configuration(t *testing.T) {
	podCIDR := PodCIDR{
		Subnet: "10.10.0.0/24",
		MaxIP:  250,
		MinIP:  2,
	}

	assert.Equal(t, "10.10.0.0/24", podCIDR.Subnet)
	assert.Equal(t, 250, podCIDR.MaxIP)
	assert.Equal(t, 2, podCIDR.MinIP)
}

func TestHTTP_SecuritySettings(t *testing.T) {
	tests := []struct {
		name     string
		http     HTTP
		insecure bool
	}{
		{
			name: "secure HTTP with CA cert",
			http: HTTP{
				Insecure: false,
				CaCert:   "/path/to/ca.crt",
			},
			insecure: false,
		},
		{
			name: "insecure HTTP",
			http: HTTP{
				Insecure: true,
			},
			insecure: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.insecure, tt.http.Insecure)
		})
	}
}

func TestNetwork_Configuration(t *testing.T) {
	network := Network{
		EnableTunnel:         true,
		WildcardDNS:          "*.example.com",
		WstunnelTemplatePath: "/path/to/template",
		WstunnelCommand:      "wstunnel client --remote-addr %s",
	}

	assert.True(t, network.EnableTunnel)
	assert.Equal(t, "*.example.com", network.WildcardDNS)
	assert.NotEmpty(t, network.WstunnelCommand)
}
