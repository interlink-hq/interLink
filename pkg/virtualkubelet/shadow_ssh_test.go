package virtualkubelet

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"
	k8stesting "k8s.io/client-go/testing"
)

const (
	testKeytabSecret = "hpc-keytab"
	testPrincipal    = "alice@EXAMPLE.ORG"
	testExtraOption  = "ConnectTimeout=7"
	testSocketPorts  = "8888"
	testRestartHint  = "letting Kubernetes restart the container"
	testNotebookPort = "notebook"
	testNotAPort     = "is not a port number"
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
				KeySecret: testSSHKeySecret,
			},
		},
	}
}

// kerberosConfig is sshConfig authenticating with a keytab instead of a key.
func kerberosConfig() Config {
	config := sshConfig()
	config.Network.SSH.KeySecret = ""
	config.Network.SSH.Auth = SSHAuthKerberos
	config.Network.SSH.KeytabSecret = testKeytabSecret
	config.Network.SSH.Principal = testPrincipal
	return config
}

// withForward sets where the forwarding session ends and how it forwards.
func withForward(config Config, target, mode string) Config {
	config.Network.SSH.ForwardTarget = target
	config.Network.SSH.ForwardMode = mode
	return config
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
		assert.Equal(t, SSHForwardTargetLogin, s.ForwardTarget,
			"upgrading with unchanged values must keep today's forward")
		assert.Equal(t, SSHNodeHostKeysVerify, s.NodeHostKeys)
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
		config := kerberosConfig()
		config.Network.SSH.Auth = "KERBEROS"

		config = normalized(t, config)
		assert.Equal(t, SSHAuthKerberos, config.Network.SSH.Auth)
		assert.Equal(t, DefaultSSHKeytabSecretKey, config.Network.SSH.KeytabSecretKey)
	})

	t.Run("forward target and node host keys ignore case and spaces", func(t *testing.T) {
		config := sshConfig()
		config.Network.SSH.ForwardTarget = " Node "
		config.Network.SSH.NodeHostKeys = "ACCEPT-NEW"

		config = normalized(t, config)
		assert.Equal(t, SSHForwardTargetNode, config.Network.SSH.ForwardTarget)
		assert.Equal(t, SSHNodeHostKeysAcceptNew, config.Network.SSH.NodeHostKeys)
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
		{
			name:    "unknown forward target",
			mutate:  func(c *Config) { c.Network.SSH.ForwardTarget = "gateway" },
			wantErr: "unknown Network.SSH.ForwardTarget",
		},
		{
			name:    "unknown node host key policy",
			mutate:  func(c *Config) { c.Network.SSH.NodeHostKeys = "no" },
			wantErr: "unknown Network.SSH.NodeHostKeys",
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
// Every container script must also parse as shell.
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
		SocketDirKey:       shadowSocketDirKey,
		NeedsSocketDir:     hasSocketPorts(ports),
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
	if deployment != nil {
		spec := deployment.Spec.Template.Spec
		for _, c := range append(spec.InitContainers, spec.Containers...) {
			assertShellParses(t, c)
		}
	}
	return manifest, deployment
}

// assertShellParses runs the container's script through `sh -n` and `bash -n`,
// so a template change that breaks the quoting fails here rather than in a
// crash-looping shadow.
func assertShellParses(t *testing.T, c v1.Container) {
	t.Helper()
	if len(c.Args) == 0 {
		return
	}
	for _, shell := range []string{"sh", "bash"} {
		path, err := exec.LookPath(shell)
		if err != nil {
			t.Logf("%s not found, not checking the %s script", shell, c.Name)
			continue
		}
		cmd := exec.Command(path, "-n")
		cmd.Stdin = strings.NewReader(c.Args[0])
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		assert.NoError(t, cmd.Run(), "%s -n on the %s script: %s", shell, c.Name, stderr.String())
	}
}

// forwardScript returns the script of the ssh-forward container.
func forwardScript(t *testing.T, deployment *appsv1.Deployment) string {
	t.Helper()
	for _, c := range deployment.Spec.Template.Spec.Containers {
		if c.Name == "ssh-forward" && len(c.Args) > 0 {
			return c.Args[0]
		}
	}
	t.Fatal("no ssh-forward container")
	return ""
}

// initScript returns the script of the wait-for-node init container.
func initScript(t *testing.T, deployment *appsv1.Deployment) string {
	t.Helper()
	for _, c := range deployment.Spec.Template.Spec.InitContainers {
		if c.Name == "wait-for-node" && len(c.Args) > 0 {
			return c.Args[0]
		}
	}
	t.Fatal("no wait-for-node init container")
	return ""
}

// optionSet returns the body of a `<name>="..."` ssh option assignment in script.
func optionSet(t *testing.T, script, name string) string {
	t.Helper()
	_, rest, found := strings.Cut(script, name+`="\`)
	if !found {
		t.Fatalf("%s is not set in the script", name)
	}
	body, _, _ := strings.Cut(rest, `"`)
	return body
}

func TestSSHShadowTemplate(t *testing.T) {
	tcp := []PortMapping{{Port: 8888, Name: testNotebookPort, Protocol: "TCP"}}

	login := withForward(sshConfig(), SSHForwardTargetLogin, "")

	t.Run("forwards each exposed port to the node the plugin reported", func(t *testing.T) {
		manifest, deployment := renderSSHShadow(t, login, tcp)

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
		manifest, deployment := renderSSHShadow(t, kerberosConfig(), tcp)

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
		manifest, deployment := renderSSHShadow(t, withForward(sshConfig(), SSHForwardTargetLogin, SSHForwardModeExec), tcp)

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
		manifest, _ := renderSSHShadow(t, login, []PortMapping{
			{Port: 8888, Name: testNotebookPort, Protocol: "TCP"},
			{Port: 9999, Name: "telemetry", Protocol: "UDP"},
		})

		assert.Contains(t, manifest, `-L "0.0.0.0:8888:$node:8888"`)
		assert.NotContains(t, manifest, "9999")
	})

	t.Run("pins host keys when a known_hosts ConfigMap is configured", func(t *testing.T) {
		config := sshConfig()
		config.Network.SSH.KnownHostsConfigMap = testKnownHostsConfigMap

		manifest, _ := renderSSHShadow(t, config, tcp)

		assert.Contains(t, manifest, "StrictHostKeyChecking=yes")
		assert.Contains(t, manifest, "UserKnownHostsFile=/interlink/known-hosts/known_hosts")
		assert.NotContains(t, manifest, "StrictHostKeyChecking=accept-new")
	})

	t.Run("falls back to accept-new without one", func(t *testing.T) {
		manifest, _ := renderSSHShadow(t, sshConfig(), tcp)
		assert.Contains(t, manifest, "StrictHostKeyChecking=accept-new")
	})
}

// socketAndTCPPorts is a notebook served on a unix socket next to a plain TCP port.
func socketAndTCPPorts() []PortMapping {
	return []PortMapping{
		{Port: 8888, Name: testNotebookPort, Protocol: DefaultProtocol, Socket: true},
		{Port: 6006, Name: "board", Protocol: DefaultProtocol},
	}
}

func TestSSHShadowTemplateNodeTarget(t *testing.T) {
	node := withForward(sshConfig(), SSHForwardTargetNode, SSHForwardModePortForward)
	nodeExec := withForward(sshConfig(), SSHForwardTargetNode, SSHForwardModeExec)

	t.Run("portforward reaches the node's sshd with -W and forwards from inside it", func(t *testing.T) {
		manifest, deployment := renderSSHShadow(t, node, socketAndTCPPorts())
		script := forwardScript(t, deployment)

		assert.Contains(t, script, `-W %s:22`)
		assert.Contains(t, script, `*:*) w_node="[$node]"`, "an IPv6 node goes to -W in brackets")
		assert.Contains(t, script, `-L "0.0.0.0:6006:127.0.0.1:6006"`)
		assert.Contains(t, script, `-L "0.0.0.0:8888:$sd/fwd-8888.sock"`)
		assert.NotContains(t, manifest, "socat")
		assert.NotContains(t, script, `:$node:`, "nothing may cross the network to the node in clear")
	})

	t.Run("exec relays the session through the login node and forwards on the node", func(t *testing.T) {
		manifest, deployment := renderSSHShadow(t, nodeExec, socketAndTCPPorts())
		script := forwardScript(t, deployment)

		assert.Contains(t, script, `"%s %s 22"`)
		assert.Contains(t, script, `-L "0.0.0.0:6006:127.0.0.1:6006"`)
		assert.Contains(t, script, `-L "0.0.0.0:8888:$sd/fwd-8888.sock"`)
		assert.NotContains(t, manifest, "socat")
		assert.NotContains(t, script, "-W ")
	})

	t.Run("waits for the socket directory only when a port needs it", func(t *testing.T) {
		_, withSocket := renderSSHShadow(t, node, socketAndTCPPorts())
		assert.Contains(t, initScript(t, withSocket), "/interlink/node/"+shadowSocketDirKey)
		assert.Contains(t, forwardScript(t, withSocket), "set SocketDirRoot in the plugin")

		_, tcpOnly := renderSSHShadow(t, node, []PortMapping{{Port: 6006, Protocol: DefaultProtocol}})
		assert.NotContains(t, initScript(t, tcpOnly), shadowSocketDirKey,
			"a plugin without SocketDirRoot must still get TCP forwards")
		assert.NotContains(t, forwardScript(t, tcpOnly), "set SocketDirRoot in the plugin")
	})

	t.Run("kerberos delegates on neither hop", func(t *testing.T) {
		_, deployment := renderSSHShadow(t, withForward(kerberosConfig(), SSHForwardTargetNode, ""), socketAndTCPPorts())
		script := forwardScript(t, deployment)

		for _, set := range []string{"ssh_opts", "node_opts"} {
			opts := optionSet(t, script, set)
			assert.Contains(t, opts, "-o GSSAPIDelegateCredentials=no", set)
			assert.NotContains(t, opts, "GSSAPIDelegateCredentials=yes", set)
		}
	})

	t.Run("extra options apply to every hop, after the fixed ones", func(t *testing.T) {
		config := withForward(kerberosConfig(), SSHForwardTargetNode, "")
		config.Network.SSH.ExtraOptions = []string{testExtraOption}

		_, deployment := renderSSHShadow(t, config, socketAndTCPPorts())
		script := forwardScript(t, deployment)

		login := optionSet(t, script, "ssh_opts")
		extra := strings.Index(login, "-o "+testExtraOption)
		assert.Greater(t, extra, strings.Index(login, "StrictHostKeyChecking"))
		// After them on the login hop only, so ExtraOptions can turn delegation back on.
		assert.Greater(t, strings.Index(login, "GSSAPIDelegateCredentials=no"), extra)

		hop := optionSet(t, script, "node_opts")
		extra = strings.Index(hop, "-o "+testExtraOption)
		assert.Greater(t, extra, strings.Index(hop, "ProxyCommand"))
		assert.Greater(t, extra, strings.Index(hop, "GSSAPIDelegateCredentials=no"))
	})

	t.Run("verify checks node keys against the ConfigMap and the login node's files", func(t *testing.T) {
		config := node
		config.Network.SSH.KnownHostsConfigMap = testKnownHostsConfigMap

		_, deployment := renderSSHShadow(t, config, socketAndTCPPorts())
		script := forwardScript(t, deployment)

		assert.Contains(t, script, "cat /etc/ssh/ssh_known_hosts")
		assert.Contains(t, script, "ssh-keygen -F")
		assert.Contains(t, script, "cat /interlink/known-hosts/known_hosts")
		assert.Regexp(t, `cat /interlink/known-hosts/known_hosts >> "\$w/node_known_hosts"\n(\s*#.*\n)*\s*echo >> "\$w/node_known_hosts"`, script,
			"a ConfigMap without a final newline must not join the login node's first line")
		assert.Contains(t, optionSet(t, script, "node_opts"), "StrictHostKeyChecking=yes")
		assert.NotContains(t, script, "StrictHostKeyChecking=accept-new")
	})

	t.Run("accept-new says so on every start", func(t *testing.T) {
		config := node
		config.Network.SSH.NodeHostKeys = SSHNodeHostKeysAcceptNew

		_, deployment := renderSSHShadow(t, config, socketAndTCPPorts())
		script := forwardScript(t, deployment)

		assert.Contains(t, script, "WARNING: the host key of $node is not verified")
		assert.Contains(t, optionSet(t, script, "node_opts"), "StrictHostKeyChecking=accept-new")
		assert.NotContains(t, script, "ssh-keygen -F")
	})

	t.Run("retries instead of crash-looping while a requeued job is away", func(t *testing.T) {
		_, deployment := renderSSHShadow(t, node, socketAndTCPPorts())
		script := forwardScript(t, deployment)

		assert.Contains(t, script, "retrying in 30s")
		assert.NotContains(t, script, testRestartHint)
	})
}

// TestSSHShadowTemplateLoginTargetUnchanged pins the login target to the forward
// it always rendered: only Kerberos delegation on the login hop changed.
func TestSSHShadowTemplateLoginTargetUnchanged(t *testing.T) {
	tcp := []PortMapping{{Port: 8888, Protocol: DefaultProtocol}}

	for _, mode := range []string{SSHForwardModePortForward, SSHForwardModeExec} {
		t.Run(mode, func(t *testing.T) {
			config := withForward(kerberosConfig(), SSHForwardTargetLogin, mode)
			config.Network.SSH.ExtraOptions = []string{testExtraOption}

			_, deployment := renderSSHShadow(t, config, tcp)
			script := forwardScript(t, deployment)

			for _, nodeOnly := range []string{"node_hop", "node_opts", "read_sockdir", "-W ", "fwd-"} {
				assert.NotContains(t, script, nodeOnly)
			}
			assert.Contains(t, script, testRestartHint)

			opts := optionSet(t, script, "ssh_opts")
			assert.NotContains(t, opts, "GSSAPIDelegateCredentials=yes")
			assert.Greater(t, strings.Index(opts, "GSSAPIDelegateCredentials=no"), strings.Index(opts, testExtraOption))
		})
	}
}

// TestSSHShadowScriptsParse renders every forward target, forward mode and auth
// method; renderSSHShadow runs each container script through the shell parser.
func TestSSHShadowScriptsParse(t *testing.T) {
	for _, target := range []string{SSHForwardTargetLogin, SSHForwardTargetNode} {
		for _, mode := range []string{SSHForwardModePortForward, SSHForwardModeExec} {
			for _, auth := range []string{SSHAuthPublicKey, SSHAuthKerberos} {
				for _, keys := range []string{SSHNodeHostKeysVerify, SSHNodeHostKeysAcceptNew} {
					t.Run(strings.Join([]string{target, mode, auth, keys}, "/"), func(t *testing.T) {
						config := sshConfig()
						if auth == SSHAuthKerberos {
							config = kerberosConfig()
						}
						config = withForward(config, target, mode)
						config.Network.SSH.NodeHostKeys = keys
						config.Network.SSH.KnownHostsConfigMap = testKnownHostsConfigMap
						config.Network.SSH.ExtraOptions = []string{testExtraOption}

						ports := []PortMapping{{Port: 6006, Protocol: DefaultProtocol}}
						if target == SSHForwardTargetNode {
							ports = socketAndTCPPorts()
						}
						renderSSHShadow(t, config, ports)
					})
				}
			}
		}
	}
}

// socketPod is an offloaded pod exposing 8888 and 6006 over TCP plus 5353 over
// UDP, and listing socketPorts in interlink.eu/shadow-socket-ports.
func socketPod(socketPorts string) *v1.Pod {
	pod := podWithPort("nb", testNamespaceDefault, "uid-1")
	pod.Annotations = map[string]string{
		"interlink.eu/wstunnel-extra-ports": "6006,5353:dns:UDP",
	}
	if socketPorts != "" {
		pod.Annotations[annShadowSocketPorts] = socketPorts
	}
	return pod
}

func TestCheckSocketPortsRequest(t *testing.T) {
	nodeTarget := normalized(t, withForward(sshConfig(), SSHForwardTargetNode, ""))

	tests := []struct {
		name    string
		config  func() Config
		ports   string
		wantErr string
	}{
		{name: "no annotation", config: func() Config { return Config{} }},
		{name: "one socket port", ports: testSocketPorts},
		{name: "a port from wstunnel-extra-ports, with spaces", ports: " 8888 , 6006 "},
		{name: "not a number", ports: "jupyter", wantErr: testNotAPort},
		{name: "empty entry", ports: "8888,,6006", wantErr: testNotAPort},
		{name: "negative", ports: "-1", wantErr: testNotAPort},
		{name: "port zero", ports: "0", wantErr: "outside 1-65535"},
		{name: "beyond 65535", ports: "65536", wantErr: "outside 1-65535"},
		{name: "a port the pod does not expose", ports: "9000", wantErr: "not an exposed TCP port"},
		{name: "a UDP port", ports: "5353", wantErr: "not an exposed TCP port"},
		{
			name:    "the wstunnel shadow",
			config:  func() Config { c := nodeTarget; c.Network.ShadowMode = ShadowModeWstunnel; return c },
			ports:   testSocketPorts,
			wantErr: "Network.ShadowMode",
		},
		{
			name:    "tunnels switched off",
			config:  func() Config { c := nodeTarget; c.Network.EnableTunnel = false; return c },
			ports:   testSocketPorts,
			wantErr: "EnableTunnel is false",
		},
		{
			name:    "the login target",
			config:  func() Config { return normalized(t, sshConfig()) },
			ports:   testSocketPorts,
			wantErr: "Network.SSH.ForwardTarget",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := nodeTarget
			if tt.config != nil {
				config = tt.config()
			}
			p := &Provider{config: config}

			err := p.checkSocketPortsRequest(socketPod(tt.ports))

			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			assert.ErrorContains(t, err, tt.wantErr, "a pod whose socket ports cannot be honoured must be refused at creation")
		})
	}
}

// TestCreatePodRefusesUnhonouredSocketPorts checks that CreatePod runs the
// check before anything else, so the pod never reaches the plugin.
func TestCreatePodRefusesUnhonouredSocketPorts(t *testing.T) {
	p := &Provider{config: normalized(t, withForward(sshConfig(), SSHForwardTargetLogin, ""))}

	err := p.CreatePod(t.Context(), socketPod(testSocketPorts))

	assert.ErrorContains(t, err, "Network.SSH.ForwardTarget")
}

// TestCreateShadowPodServesListedPortsOnSockets runs createShadowPod against a
// fake API server already holding the credential and the shadow's pod, and checks
// what the template received.
func TestCreateShadowPodServesListedPortsOnSockets(t *testing.T) {
	config := normalized(t, withForward(sshConfig(), SSHForwardTargetNode, ""))
	pod := socketPod(testSocketPorts)
	identity, err := computeShadowResourceIdentity(pod)
	require.NoError(t, err)
	key := &v1.Secret{ObjectMeta: metav1.ObjectMeta{Name: testSSHKeySecret, Namespace: config.Namespace}}
	running := &v1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name:      identity.Name + "-0",
		Namespace: identity.Namespace,
		Labels:    map[string]string{"app.kubernetes.io/component": identity.Name},
	}}
	p := &Provider{clientSet: fake.NewSimpleClientset(key, running), config: config}

	_, data, err := p.createShadowPod(t.Context(), pod)

	require.NoError(t, err)
	// extractPortMappings walks a map, so the order is not fixed.
	assert.ElementsMatch(t, []PortMapping{
		{Port: 8888, Protocol: DefaultProtocol, Socket: true},
		{Port: 6006, Protocol: DefaultProtocol},
		{Port: 5353, Name: "dns", Protocol: "UDP"},
	}, data.ExposedPorts)
	assert.True(t, data.NeedsSocketDir, "the shadow must wait for the socket directory")
}

func TestMarkSocketPorts(t *testing.T) {
	sockets := func(pod *v1.Pod) []int32 {
		var marked []int32
		for _, m := range shadowPortMappings(pod) {
			if m.Socket {
				marked = append(marked, m.Port)
			}
		}
		return marked
	}

	assert.Equal(t, []int32{8888}, sockets(socketPod(testSocketPorts)))
	assert.ElementsMatch(t, []int32{8888, 6006}, sockets(socketPod("8888,6006")))
	assert.Empty(t, sockets(socketPod("")))
	assert.Empty(t, sockets(socketPod("5353")), "a UDP port is never a socket")
	assert.Empty(t, sockets(socketPod("8888,x")), "a malformed list marks nothing")

	assert.True(t, hasSocketPorts(socketAndTCPPorts()))
	assert.False(t, hasSocketPorts([]PortMapping{{Port: 6006, Protocol: DefaultProtocol}}))
}

func TestReplicateShadowCredentials(t *testing.T) {
	const source = "interlink"
	target := shadowResourceIdentity{Name: "shadow-nb-user", Namespace: "user"}

	newClient := func() *fake.Clientset {
		return fake.NewSimpleClientset(
			&v1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: testSSHKeySecret, Namespace: source},
				Data:       map[string][]byte{DefaultSSHKeySecretKey: []byte("PRIVATE KEY")},
			},
			&v1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: testKnownHostsConfigMap, Namespace: source},
				Data:       map[string]string{"known_hosts": "login.hpc.example.org ssh-ed25519 AAAA"},
			},
		)
	}

	t.Run("copies the credential into the shadow namespace", func(t *testing.T) {
		config := sshConfig()
		config.Network.SSH.KnownHostsConfigMap = testKnownHostsConfigMap
		client := newClient()
		p := &Provider{clientSet: client, config: normalized(t, config)}

		assert.NoError(t, p.replicateShadowCredentials(t.Context(), target))

		secret, err := client.CoreV1().Secrets(target.Namespace).Get(t.Context(), testSSHKeySecret, metav1.GetOptions{})
		assert.NoError(t, err)
		assert.Equal(t, []byte("PRIVATE KEY"), secret.Data[DefaultSSHKeySecretKey])

		cm, err := client.CoreV1().ConfigMaps(target.Namespace).Get(t.Context(), testKnownHostsConfigMap, metav1.GetOptions{})
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

		_, err := client.CoreV1().Secrets(target.Namespace).Get(t.Context(), testSSHKeySecret, metav1.GetOptions{})
		assert.Error(t, err, "the operator opted out; nothing should be copied")
	})

	t.Run("reports a missing source credential instead of rendering a broken shadow", func(t *testing.T) {
		p := &Provider{clientSet: fake.NewSimpleClientset(), config: normalized(t, sshConfig())}

		err := p.replicateShadowCredentials(t.Context(), target)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), testSSHKeySecret)
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

// TestReplicateCredentialsRefusesToClobber covers the multi-tenant case: with
// interlink.eu/shadow-same-ns the shadow lands in the offloaded pod's own
// namespace, so a Secret the owner already has under the credential's name must
// not be silently replaced with the HPC key.
func TestReplicateCredentialsRefusesToClobber(t *testing.T) {
	const source = "interlink"
	const target = "alice"

	config := normalized(t, sshConfig())
	credential := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: testSSHKeySecret, Namespace: source},
		Data:       map[string][]byte{DefaultSSHKeySecretKey: []byte("hpc-private-key")},
	}

	t.Run("a secret the user already owns is left alone", func(t *testing.T) {
		theirs := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: testSSHKeySecret, Namespace: target},
			Data:       map[string][]byte{"theirs": []byte("do not lose me")},
		}
		client := fake.NewSimpleClientset(credential, theirs)
		p := &Provider{clientSet: client, config: config}

		err := p.replicateShadowCredentials(t.Context(), shadowResourceIdentity{Name: testShadowName, Namespace: target})

		assert.ErrorContains(t, err, "refusing to overwrite")
		kept, getErr := client.CoreV1().Secrets(target).Get(t.Context(), testSSHKeySecret, metav1.GetOptions{})
		assert.NoError(t, getErr)
		assert.Equal(t, []byte("do not lose me"), kept.Data["theirs"])
	})

	// A copy made by a version that predates the marker has no annotation, but its
	// content matches, so upgrading must not start failing every offloaded pod.
	t.Run("an unmarked copy with identical content is adopted", func(t *testing.T) {
		unmarked := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: testSSHKeySecret, Namespace: target},
			Data:       map[string][]byte{DefaultSSHKeySecretKey: []byte("hpc-private-key")},
		}
		client := fake.NewSimpleClientset(credential, unmarked)
		p := &Provider{clientSet: client, config: config}

		err := p.replicateShadowCredentials(t.Context(), shadowResourceIdentity{Name: testShadowName, Namespace: target})

		assert.NoError(t, err, "an identical copy loses nothing by being rewritten")
		adopted, getErr := client.CoreV1().Secrets(target).Get(t.Context(), testSSHKeySecret, metav1.GetOptions{})
		assert.NoError(t, getErr)
		assert.Equal(t, source, adopted.Annotations[shadowReplicatedFromAnnotation])
	})

	t.Run("a copy interLink made earlier is refreshed", func(t *testing.T) {
		client := fake.NewSimpleClientset(credential)
		p := &Provider{clientSet: client, config: config}
		identity := shadowResourceIdentity{Name: testShadowName, Namespace: target}

		assert.NoError(t, p.replicateShadowCredentials(t.Context(), identity))
		first, err := client.CoreV1().Secrets(target).Get(t.Context(), testSSHKeySecret, metav1.GetOptions{})
		assert.NoError(t, err)
		assert.Equal(t, source, first.Annotations[shadowReplicatedFromAnnotation])

		// a second offloaded pod in the same namespace must not trip the guard
		assert.NoError(t, p.replicateShadowCredentials(t.Context(), identity))
	})
}

// TestReplicateCredentialsAfterTypeChange covers a source Secret recreated with
// another type, as kubernetes.io/ssh-auth: the API server refuses that change on
// update, so the copy must be created again rather than updated.
func TestReplicateCredentialsAfterTypeChange(t *testing.T) {
	const source = "interlink"
	const target = "alice"
	secrets := v1.SchemeGroupVersion.WithResource("secrets")

	credential := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: testSSHKeySecret, Namespace: source},
		Type:       v1.SecretTypeSSHAuth,
		Data:       map[string][]byte{v1.SSHAuthPrivateKey: []byte("new-key")},
	}
	copyOf := func(secretType v1.SecretType, key, uid string) *v1.Secret {
		meta := replicatedMeta(testSSHKeySecret, target, source)
		meta.UID = types.UID(uid)
		meta.ResourceVersion = uid
		return &v1.Secret{ObjectMeta: meta, Type: secretType, Data: map[string][]byte{v1.SSHAuthPrivateKey: []byte(key)}}
	}
	unmarked := copyOf(v1.SecretTypeSSHAuth, "their-key", "unmarked")
	unmarked.Annotations = nil
	stale := func(stored runtime.Object, uid *types.UID, rv *string) bool {
		meta := stored.(*v1.Secret).ObjectMeta
		return (uid != nil && meta.UID != *uid) || (rv != nil && *rv != "" && meta.ResourceVersion != *rv)
	}
	newProvider := func(client *fake.Clientset) *Provider {
		// the fake clientset accepts any update; refuse a stale resourceVersion with 409
		// and a type change as the API server does
		client.PrependReactor("update", "secrets", func(action k8stesting.Action) (bool, runtime.Object, error) {
			updated := action.(k8stesting.UpdateAction).GetObject().(*v1.Secret)
			stored, err := client.Tracker().Get(secrets, updated.Namespace, updated.Name)
			if err != nil {
				return false, nil, nil
			}
			if stale(stored, nil, &updated.ResourceVersion) {
				return true, nil, apierrors.NewConflict(secrets.GroupResource(), updated.Name, errors.New("object has been modified"))
			}
			if stored.(*v1.Secret).Type != updated.Type {
				return true, nil, apierrors.NewInvalid(v1.SchemeGroupVersion.WithKind("Secret").GroupKind(), updated.Name,
					field.ErrorList{field.Invalid(field.NewPath("type"), updated.Type, "field is immutable")})
			}
			return false, nil, nil
		})
		// the fake tracker ignores delete preconditions; refuse stale ones with 409 as the API server does
		client.PrependReactor("delete", "secrets", func(action k8stesting.Action) (bool, runtime.Object, error) {
			deleted := action.(k8stesting.DeleteAction)
			pre := action.(interface{ GetDeleteOptions() metav1.DeleteOptions }).GetDeleteOptions().Preconditions
			if pre == nil {
				return false, nil, nil
			}
			stored, err := client.Tracker().Get(secrets, deleted.GetNamespace(), deleted.GetName())
			if err == nil && stale(stored, pre.UID, pre.ResourceVersion) {
				return true, nil, apierrors.NewConflict(secrets.GroupResource(), deleted.GetName(), errors.New("precondition failed"))
			}
			return false, nil, nil
		})
		config := normalized(t, sshConfig())
		config.Network.SSH.KeySecretKey = v1.SSHAuthPrivateKey
		return &Provider{clientSet: client, config: config}
	}
	assertCopied := func(t *testing.T, client *fake.Clientset) {
		copied, err := client.CoreV1().Secrets(target).Get(t.Context(), testSSHKeySecret, metav1.GetOptions{})
		require.NoError(t, err)
		assert.Equal(t, v1.SecretTypeSSHAuth, copied.Type)
		assert.Equal(t, []byte("new-key"), copied.Data[v1.SSHAuthPrivateKey])
		assert.Equal(t, source, copied.Annotations[shadowReplicatedFromAnnotation])
	}
	identity := shadowResourceIdentity{Name: testShadowName, Namespace: target}

	t.Run("a copy of the old type is created again", func(t *testing.T) {
		client := fake.NewSimpleClientset(credential, copyOf(v1.SecretTypeOpaque, "old-key", "old"))
		p := newProvider(client)

		require.NoError(t, p.replicateShadowCredentials(t.Context(), identity))
		assertCopied(t, client)
	})

	// Another writer replaced the copy between the read and the delete: the delete
	// still names the old UID, so it gets 409 and their copy stays.
	t.Run("a copy another writer replaced in the meantime is kept and updated", func(t *testing.T) {
		client := fake.NewSimpleClientset(credential, copyOf(v1.SecretTypeOpaque, "old-key", "old"))
		p := newProvider(client)
		client.PrependReactor("delete", "secrets", func(_ k8stesting.Action) (bool, runtime.Object, error) {
			return false, nil, client.Tracker().Update(secrets, copyOf(v1.SecretTypeSSHAuth, "new-key", "theirs"), target)
		})

		require.NoError(t, p.replicateShadowCredentials(t.Context(), identity))
		assertCopied(t, client)
		kept, err := client.CoreV1().Secrets(target).Get(t.Context(), testSSHKeySecret, metav1.GetOptions{})
		require.NoError(t, err)
		assert.Equal(t, types.UID("theirs"), kept.UID, "the other writer's copy must not be deleted")
	})

	assertUntouched := func(t *testing.T, client *fake.Clientset, err error) {
		require.ErrorContains(t, err, "refusing to overwrite")
		kept, getErr := client.CoreV1().Secrets(target).Get(t.Context(), testSSHKeySecret, metav1.GetOptions{})
		require.NoError(t, getErr)
		assert.Equal(t, unmarked, kept)
	}

	// An unmarked Secret with other data replaced the copy between the read and the
	// delete: the delete gets 409 and the Secret is checked again, then refused.
	t.Run("an unmarked Secret that replaced the copy before the delete is not overwritten", func(t *testing.T) {
		client := fake.NewSimpleClientset(credential, copyOf(v1.SecretTypeOpaque, "old-key", "old"))
		p := newProvider(client)
		client.PrependReactor("delete", "secrets", func(_ k8stesting.Action) (bool, runtime.Object, error) {
			return false, nil, client.Tracker().Update(secrets, unmarked.DeepCopy(), target)
		})

		assertUntouched(t, client, p.replicateShadowCredentials(t.Context(), identity))
	})

	// An unmarked Secret appeared between the delete and the create: the create gets
	// AlreadyExists and the Secret is checked again, then refused.
	t.Run("an unmarked Secret created after the delete is not overwritten", func(t *testing.T) {
		client := fake.NewSimpleClientset(credential, copyOf(v1.SecretTypeOpaque, "old-key", "old"))
		p := newProvider(client)
		client.PrependReactor("create", "secrets", func(_ k8stesting.Action) (bool, runtime.Object, error) {
			return false, nil, client.Tracker().Add(unmarked.DeepCopy())
		})

		assertUntouched(t, client, p.replicateShadowCredentials(t.Context(), identity))
	})
}
