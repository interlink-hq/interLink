package virtualkubelet

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecuteWstunnelTemplate_UsesFullMeshBridgeTemplate(t *testing.T) {
	p := Provider{
		config: Config{
			Network: Network{
				FullMesh: true,
			},
		},
	}

	manifest, err := p.executeWstunnelTemplate(context.Background(), WstunnelTemplateData{
		Name:            "bridge-test",
		Namespace:       "bridge-test-wstunnel",
		RandomPassword:  "secret",
		WildcardDNS:     "example.invalid",
		WGPrivateKey:    "server-private-key",
		ClientPublicKey: "client-public-key",
		PVCNFSClaimName: "shared-data",
		PVCNFSServer:    "10.0.0.15",
		PVCNFSPath:      "/exports/shared-data",
		PVCBridgePath:   "/tmp/interlink-pvc-bridge/pod-uid/shared-data",
		FuseNFSURL:      "https://example.invalid/fuse-nfs",
		SSHPublicKeyURL: "https://example.invalid/id_ed25519.pub",
	})
	require.NoError(t, err)
	assert.Contains(t, manifest, "Starting PVC bridge for claim shared-data")
	assert.Contains(t, manifest, "nfs://10.0.0.15/exports/shared-data")
	assert.Contains(t, manifest, "mkdir -p /tmp/interlink-pvc-bridge/pod-uid/shared-data")
	assert.Contains(t, manifest, "curl -L -f -k https://example.invalid/id_ed25519.pub")
	assert.Contains(t, manifest, "ListenAddress 10.7.0.1")
}

func TestGenerateFullMeshScript_IncludesSSHFSBridge(t *testing.T) {
	serverPriv, _, err := generateWGKeypair()
	require.NoError(t, err)
	clientPriv, _, err := generateWGKeypair()
	require.NoError(t, err)

	p := Provider{
		config: Config{
			Network: Network{
				FullMesh:         true,
				SSHFSURL:         "https://example.invalid/sshfs-bin",
				SSHPrivateKeyURL: "https://example.invalid/id_ed25519",
				DNSServiceIP:     "10.43.0.10",
				PodCIDRCluster:   "10.42.0.0/16",
				ServiceCIDR:      "10.43.0.0/16",
			},
		},
	}

	script, err := p.generateFullMeshScript(context.Background(), &WstunnelTemplateData{
		RandomPassword:   "secret",
		WGPrivateKey:     serverPriv,
		ClientPrivateKey: clientPriv,
		WGMTU:            1280,
		KeepaliveSecs:    25,
		PVCNFSClaimName:  "shared-data",
		PVCNFSServer:     "10.0.0.15",
		PVCNFSPath:       "/exports/shared-data",
		PVCBridgePath:    "/tmp/interlink-pvc-bridge/12345678-1234-1234-1234-123456789abc/shared-data",
	}, "bridge-test.example.invalid", "12345678-1234-1234-1234-123456789abc")
	require.NoError(t, err)
	assert.Contains(t, script, "=== Mounting bridged PVC shared-data over SSHFS ===")
	assert.Contains(t, script, "https://example.invalid/sshfs-bin")
	assert.Contains(t, script, "https://example.invalid/id_ed25519")
	assert.Contains(t, script, "root@10.7.0.1:/tmp/interlink-pvc-bridge/12345678-1234-1234-1234-123456789abc/shared-data")
}
