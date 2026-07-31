package virtualkubelet

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"
)

const (
	testKeytabSecret = "hpc-keytab"
	testPrincipal    = "alice@EXAMPLE.ORG"
)

func sshConfig() Config {
	return Config{
		Namespace: "interlink",
		Network: Network{
			EnableTunnel: true,
			ShadowMode:   ShadowModeSSH,
			SSH: SSHTunnel{
				LoginHost: "login.hpc.example.org",
				User:      "alice",
				KeySecret: "hpc-ssh-key",
			},
		},
	}
}

func normalized(t *testing.T, config Config) Config {
	t.Helper()
	assert.NoError(t, NormalizeShadowConfig(&config))
	return config
}

func TestNormalizeShadowConfigDefaults(t *testing.T) {
	t.Run("defaults to the wstunnel shadow", func(t *testing.T) {
		config := Config{}
		assert.NoError(t, NormalizeShadowConfig(&config))
		assert.Equal(t, ShadowModeWstunnel, config.Network.ShadowMode)
	})

	t.Run("fills in ssh defaults", func(t *testing.T) {
		config := normalized(t, sshConfig())
		s := config.Network.SSH

		assert.Equal(t, DefaultSSHPort, s.Port)
		assert.Equal(t, SSHAuthPublicKey, s.Auth)
		assert.Equal(t, DefaultSSHKeySecretKey, s.KeySecretKey)
		assert.Equal(t, DefaultSSHNodeWaitTimeout, s.NodeWaitTimeout)
		assert.Equal(t, SSHForwardModePortForward, s.ForwardMode)
		assert.Contains(t, s.Image, "ssh-tunnel")
		assert.NotNil(t, s.ReplicateCredentials)
		assert.True(t, *s.ReplicateCredentials, "credentials replicate by default so per-user namespaces work")
	})

	t.Run("exec forward mode gets a relay command", func(t *testing.T) {
		config := sshConfig()
		config.Network.SSH.ForwardMode = "EXEC"

		config = normalized(t, config)
		assert.Equal(t, SSHForwardModeExec, config.Network.SSH.ForwardMode)
		assert.Equal(t, DefaultSSHExecConnectCommand, config.Network.SSH.ExecConnectCommand)
	})

	t.Run("kerberos defaults", func(t *testing.T) {
		config := sshConfig()
		config.Network.SSH.KeySecret = ""
		config.Network.SSH.Auth = "KERBEROS"
		config.Network.SSH.KeytabSecret = testKeytabSecret
		config.Network.SSH.Principal = testPrincipal

		config = normalized(t, config)
		assert.Equal(t, SSHAuthKerberos, config.Network.SSH.Auth)
		assert.Equal(t, DefaultSSHKeytabSecretKey, config.Network.SSH.KeytabSecretKey)
	})
}

func TestNormalizeShadowConfigRejects(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{
			name:    "unknown shadow mode",
			mutate:  func(c *Config) { c.Network.ShadowMode = "carrier-pigeon" },
			wantErr: "unknown Network.ShadowMode",
		},
		{
			// The mesh needs the compute node to dial the cluster, which is precisely
			// what an ssh-only site cannot do.
			name:    "ssh together with full mesh",
			mutate:  func(c *Config) { c.Network.FullMesh = true },
			wantErr: "FullMesh is not supported",
		},
		{
			name:    "missing login host",
			mutate:  func(c *Config) { c.Network.SSH.LoginHost = "" },
			wantErr: "LoginHost is required",
		},
		{
			name:    "missing user",
			mutate:  func(c *Config) { c.Network.SSH.User = "" },
			wantErr: "User is required",
		},
		{
			name:    "public key auth without a key secret",
			mutate:  func(c *Config) { c.Network.SSH.KeySecret = "" },
			wantErr: "KeySecret is required",
		},
		{
			name: "kerberos without a keytab",
			mutate: func(c *Config) {
				c.Network.SSH.Auth = SSHAuthKerberos
				c.Network.SSH.Principal = testPrincipal
			},
			wantErr: "KeytabSecret is required",
		},
		{
			name: "kerberos without a principal",
			mutate: func(c *Config) {
				c.Network.SSH.Auth = SSHAuthKerberos
				c.Network.SSH.KeytabSecret = testKeytabSecret
			},
			wantErr: "Principal is required",
		},
		{
			name:    "unknown auth method",
			mutate:  func(c *Config) { c.Network.SSH.Auth = "password" },
			wantErr: "unknown Network.SSH.Auth",
		},
		{
			name:    "unparseable node wait timeout",
			mutate:  func(c *Config) { c.Network.SSH.NodeWaitTimeout = "forever" },
			wantErr: "invalid Network.SSH.NodeWaitTimeout",
		},
		{
			name:    "unknown forward mode",
			mutate:  func(c *Config) { c.Network.SSH.ForwardMode = "smoke-signal" },
			wantErr: "unknown Network.SSH.ForwardMode",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := sshConfig()
			tt.mutate(&config)

			err := NormalizeShadowConfig(&config)

			assert.Error(t, err, "a misconfiguration must fail at startup, not at first offload")
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// renderSSHShadow renders the ssh template and returns the manifest plus the
// Deployment decoded from it, using the same decoder applyShadowManifests uses.
func renderSSHShadow(t *testing.T, config Config, ports []PortMapping) (string, *appsv1.Deployment) {
	t.Helper()
	config = normalized(t, config)
	p := &Provider{config: config}

	manifest, err := p.executeShadowTemplate(t.Context(), ShadowTemplateData{
		Name:               "shadow-nb-user",
		Namespace:          "user",
		ExposedPorts:       ports,
		NodeConfigMap:      "shadow-nb-user-node",
		NodeNameKey:        shadowNodeNameKey,
		SSH:                config.Network.SSH,
		SSHNodeWaitSeconds: p.sshNodeWaitSeconds(),
	})
	assert.NoError(t, err)

	decoder := serializer.NewCodecFactory(scheme.Scheme).UniversalDeserializer()
	var deployment *appsv1.Deployment
	for _, doc := range strings.Split(manifest, "---") {
		if strings.TrimSpace(doc) == "" {
			continue
		}
		obj, _, err := decoder.Decode([]byte(doc), nil, nil)
		assert.NoError(t, err, "every rendered document must decode:\n%s", doc)
		if d, ok := obj.(*appsv1.Deployment); ok {
			deployment = d
		}
	}
	assert.NotNil(t, deployment, "the ssh template must render a Deployment")
	return manifest, deployment
}

func TestSSHShadowTemplate(t *testing.T) {
	tcp := []PortMapping{{Port: 8888, Name: "notebook", Protocol: "TCP"}}

	t.Run("forwards each exposed port to the node the plugin reported", func(t *testing.T) {
		manifest, deployment := renderSSHShadow(t, sshConfig(), tcp)

		assert.Contains(t, manifest, `-L "0.0.0.0:8888:$node:8888"`)
		assert.Contains(t, manifest, "alice@login.hpc.example.org")
		assert.Contains(t, manifest, "-i /interlink/ssh/"+DefaultSSHKeySecretKey)
		// The node arrives through a mounted ConfigMap, not the pod spec: restarting
		// the shadow would change the pod IP already reported for the offloaded pod.
		assert.Contains(t, manifest, "shadow-nb-user-node")

		spec := deployment.Spec.Template.Spec
		assert.Equal(t, "linux", spec.NodeSelector["kubernetes.io/os"],
			"the shadow must not be scheduled onto the virtual node it shadows")
		assert.Empty(t, spec.Tolerations)
	})

	t.Run("waits for the compute node in an init container", func(t *testing.T) {
		_, deployment := renderSSHShadow(t, sshConfig(), tcp)

		names := make([]string, 0, len(deployment.Spec.Template.Spec.InitContainers))
		for _, c := range deployment.Spec.Template.Spec.InitContainers {
			names = append(names, c.Name)
		}
		assert.Contains(t, names, "wait-for-node",
			"a queued job is the normal case; the shadow shows Init rather than crash-looping")
	})

	t.Run("kerberos renders kinit and no ssh key", func(t *testing.T) {
		config := sshConfig()
		config.Network.SSH.KeySecret = ""
		config.Network.SSH.Auth = SSHAuthKerberos
		config.Network.SSH.KeytabSecret = testKeytabSecret
		config.Network.SSH.Principal = testPrincipal

		manifest, deployment := renderSSHShadow(t, config, tcp)

		assert.Contains(t, manifest, "kinit -k -t /interlink/keytab/"+DefaultSSHKeytabSecretKey+" alice@EXAMPLE.ORG")
		assert.Contains(t, manifest, "GSSAPIAuthentication=yes")
		assert.NotContains(t, manifest, "/interlink/ssh/")

		var initNames, names []string
		for _, c := range deployment.Spec.Template.Spec.InitContainers {
			initNames = append(initNames, c.Name)
		}
		for _, c := range deployment.Spec.Template.Spec.Containers {
			names = append(names, c.Name)
		}
		// kinit runs after the wait so the ticket is fresh however long the queue was,
		// and the renewer keeps it alive for the life of the tunnel.
		assert.Equal(t, []string{"wait-for-node", "kinit"}, initNames)
		assert.Contains(t, names, "kinit-renew")
	})

	t.Run("exec mode relays through the login node instead of forwarding", func(t *testing.T) {
		config := sshConfig()
		config.Network.SSH.ForwardMode = SSHForwardModeExec

		manifest, deployment := renderSSHShadow(t, config, tcp)

		// No -L anywhere: sites running the exec mode are exactly the ones whose sshd
		// refuses to open a forwarded channel at all.
		assert.NotContains(t, manifest, "-L \"0.0.0.0:")
		assert.Contains(t, manifest, "socat TCP-LISTEN:8888,fork,reuseaddr,bind=0.0.0.0")
		// the node is single-quoted for the login node's shell, which re-parses it
		assert.Contains(t, manifest, `remote_cmd="$connect_cmd '$node'"`)
		// One multiplexed connection, or every request would pay an SSH handshake and
		// the login node would see a session per connection.
		assert.Contains(t, manifest, "ssh -M -N -o ControlMaster=yes")
		assert.Contains(t, manifest, "ControlPath=%s")

		// The Service still fronts the same containerPort, so nothing above the socket
		// can tell the two modes apart.
		container := deployment.Spec.Template.Spec.Containers[0]
		assert.Equal(t, int32(8888), container.Ports[0].ContainerPort)
	})

	t.Run("skips UDP ports, which ssh -L cannot carry", func(t *testing.T) {
		manifest, _ := renderSSHShadow(t, sshConfig(), []PortMapping{
			{Port: 8888, Name: "notebook", Protocol: "TCP"},
			{Port: 9999, Name: "telemetry", Protocol: "UDP"},
		})

		assert.Contains(t, manifest, `-L "0.0.0.0:8888:$node:8888"`)
		assert.NotContains(t, manifest, "9999")
	})

	t.Run("pins host keys when a known_hosts ConfigMap is configured", func(t *testing.T) {
		config := sshConfig()
		config.Network.SSH.KnownHostsConfigMap = "hpc-known-hosts"

		manifest, _ := renderSSHShadow(t, config, tcp)

		assert.Contains(t, manifest, "StrictHostKeyChecking=yes")
		assert.Contains(t, manifest, "UserKnownHostsFile=/interlink/known-hosts/known_hosts")
		assert.NotContains(t, manifest, "accept-new")
	})

	t.Run("falls back to accept-new without one", func(t *testing.T) {
		manifest, _ := renderSSHShadow(t, sshConfig(), tcp)
		assert.Contains(t, manifest, "StrictHostKeyChecking=accept-new")
	})
}

func TestReplicateShadowCredentials(t *testing.T) {
	const source = "interlink"
	target := shadowResourceIdentity{Name: "shadow-nb-user", Namespace: "user"}

	newClient := func() *fake.Clientset {
		return fake.NewSimpleClientset(
			&v1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "hpc-ssh-key", Namespace: source},
				Data:       map[string][]byte{DefaultSSHKeySecretKey: []byte("PRIVATE KEY")},
			},
			&v1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "hpc-known-hosts", Namespace: source},
				Data:       map[string]string{"known_hosts": "login.hpc.example.org ssh-ed25519 AAAA"},
			},
		)
	}

	t.Run("copies the credential into the shadow namespace", func(t *testing.T) {
		config := sshConfig()
		config.Network.SSH.KnownHostsConfigMap = "hpc-known-hosts"
		client := newClient()
		p := &Provider{clientSet: client, config: normalized(t, config)}

		assert.NoError(t, p.replicateShadowCredentials(t.Context(), target))

		secret, err := client.CoreV1().Secrets(target.Namespace).Get(t.Context(), "hpc-ssh-key", metav1.GetOptions{})
		assert.NoError(t, err)
		assert.Equal(t, []byte("PRIVATE KEY"), secret.Data[DefaultSSHKeySecretKey])

		cm, err := client.CoreV1().ConfigMaps(target.Namespace).Get(t.Context(), "hpc-known-hosts", metav1.GetOptions{})
		assert.NoError(t, err)
		assert.Contains(t, cm.Data["known_hosts"], "ssh-ed25519")
	})

	t.Run("does nothing when replication is switched off", func(t *testing.T) {
		config := normalized(t, sshConfig())
		off := false
		config.Network.SSH.ReplicateCredentials = &off
		client := newClient()
		p := &Provider{clientSet: client, config: config}

		assert.NoError(t, p.replicateShadowCredentials(t.Context(), target))

		_, err := client.CoreV1().Secrets(target.Namespace).Get(t.Context(), "hpc-ssh-key", metav1.GetOptions{})
		assert.Error(t, err, "the operator opted out; nothing should be copied")
	})

	t.Run("reports a missing source credential instead of rendering a broken shadow", func(t *testing.T) {
		p := &Provider{clientSet: fake.NewSimpleClientset(), config: normalized(t, sshConfig())}

		err := p.replicateShadowCredentials(t.Context(), target)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "hpc-ssh-key")
	})

	t.Run("is a no-op when the shadow already lives in the source namespace", func(t *testing.T) {
		client := newClient()
		p := &Provider{clientSet: client, config: normalized(t, sshConfig())}

		assert.NoError(t, p.replicateShadowCredentials(t.Context(), shadowResourceIdentity{Name: "s", Namespace: source}))
	})
}

// TestDefaultSSHNodeWaitMatchesTimeout keeps the duration fallback in step with the
// string default that ends up in the rendered manifest.
func TestDefaultSSHNodeWaitMatchesTimeout(t *testing.T) {
	parsed, err := time.ParseDuration(DefaultSSHNodeWaitTimeout)
	assert.NoError(t, err)
	assert.Equal(t, parsed, defaultSSHNodeWait)
}
