package virtualkubelet

import (
	"bytes"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
)

const (
	reverseTestNamespace = "user"
	reverseTestService   = "minio.user.svc.cluster.local"
	reverseTestForward   = "minio.user.svc:9000"
	reverseTestPodVPN    = "interlink.eu/pod-vpn"
	reverseTestBind      = "127.77.1.2"
	reverseTestUID       = "0d9b2d4a-6f6e-4a5f-9a66-2c1c5a4e9f10"
	reverseTestSocks     = "1080"
	reverseTestContainer = "ssh-reverse"
	reverseTestReady     = "rev-relay.ready"
	reverseTestUserspace = `"%s -i -e -f %s/rev-sshd_config"`
)

func reverseConfig() Config {
	config := sshConfig()
	config.Network.SSH.Reverse.Enabled = true
	return config
}

func reversePod(annotations map[string]string) *v1.Pod {
	return &v1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name:        "trainer",
		Namespace:   reverseTestNamespace,
		UID:         reverseTestUID,
		Annotations: annotations,
	}}
}

func TestParseReverseTunnel(t *testing.T) {
	cfg := normalized(t, reverseConfig()).Network.SSH.Reverse

	t.Run("defaults", func(t *testing.T) {
		pod := reversePod(map[string]string{annReverseForwards: reverseTestService + ":9000"})
		rt, err := parseReverseTunnel(pod, cfg)
		assert.NoError(t, err)
		assert.Equal(t, []ReverseForward{{ListenPort: 9000, Host: reverseTestService, Port: 9000}}, rt.Forwards)
		assert.Zero(t, rt.SocksPort, "SOCKS is off unless asked for")
		assert.Equal(t, SSHReverseNodeExecSSH, rt.NodeExec)
		assert.Equal(t, SSHReverseSshdNode, rt.Sshd)
		assert.Equal(t, DefaultSSHReverseSshdPath, rt.SshdPath)
		assert.Equal(t, string(pod.UID), rt.JobName)
		assert.Equal(t, reverseBindAddress(string(pod.UID)), rt.Bind)
	})

	t.Run("remapped listen port, socks and bind", func(t *testing.T) {
		pod := reversePod(map[string]string{
			annReverseForwards:  "6443:kubernetes.default.svc.cluster.local:443 redis.user.svc:6379",
			annReverseSocksPort: reverseTestSocks,
			annReverseBind:      reverseTestBind,
		})
		rt, err := parseReverseTunnel(pod, cfg)
		assert.NoError(t, err)
		assert.Equal(t, []ReverseForward{
			{ListenPort: 6443, Host: "kubernetes.default.svc.cluster.local", Port: 443},
			{ListenPort: 6379, Host: "redis.user.svc", Port: 6379},
		}, rt.Forwards)
		assert.Equal(t, int32(1080), rt.SocksPort)
		assert.Equal(t, reverseTestBind, rt.Bind)
	})

	t.Run("the removed owner-only and node-exec annotations are ignored", func(t *testing.T) {
		pod := reversePod(map[string]string{
			annReverseForwards:                reverseTestService + ":9000",
			"interlink.eu/reverse-owner-only": "false",
			"interlink.eu/reverse-node-exec":  "srun",
		})
		rt, err := parseReverseTunnel(pod, cfg)
		assert.NoError(t, err, "pods written for the draft must still start")
		assert.Equal(t, SSHReverseNodeExecSSH, rt.NodeExec, "the carrier is the site's decision")
	})

	rejected := []struct {
		name        string
		annotations map[string]string
		wantErr     string
	}{
		{"malformed forward", map[string]string{annReverseForwards: "minio"}, "expected host:port"},
		{"privileged port without a listen port", map[string]string{annReverseForwards: "kubernetes.default.svc:443"}, "below 1024"},
		{"host that is not a name", map[string]string{annReverseForwards: "minio;rm:9000"}, "not a hostname"},
		{"two forwards on one listen port", map[string]string{annReverseForwards: "a.user.svc:9000 9000:b.user.svc:9001"}, "both listen on port 9000"},
		{"socks on a forward's port", map[string]string{annReverseForwards: "a.user.svc:9000", annReverseSocksPort: "9000"}, "already used"},
		{"socks on a privileged port", map[string]string{annReverseSocksPort: "80"}, "below 1024"},
		{"bind outside loopback", map[string]string{annReverseSocksPort: reverseTestSocks, annReverseBind: "10.0.0.1"}, "127.x.y.z"},
		{"bind on the shared loopback address", map[string]string{annReverseSocksPort: reverseTestSocks, annReverseBind: "127.0.0.1"}, "127.x.y.z"},
	}
	for _, tt := range rejected {
		t.Run("rejects "+tt.name, func(t *testing.T) {
			_, err := parseReverseTunnel(reversePod(tt.annotations), cfg)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestReverseBindAddress(t *testing.T) {
	a := reverseBindAddress(reverseTestUID)
	b := reverseBindAddress("6c0f1e3a-1b2c-4d5e-8f90-a1b2c3d4e5f6")
	assert.Equal(t, a, reverseBindAddress(reverseTestUID), "the address must not change across restarts")
	assert.NotEqual(t, a, b, "two pods on one node need distinct addresses")
	for _, addr := range []string{a, b} {
		assert.Regexp(t, `^127\.([1-9]|[1-9][0-9]|1[0-9]{2}|2[0-4][0-9]|25[0-4])\.[0-9]{1,3}\.([1-9]|[1-9][0-9]|1[0-9]{2}|2[0-4][0-9]|25[0-4])$`, addr,
			"stay out of 127.0.0.0/24 and off .0 and .255")
	}
}

// assertBashParses runs script through `bash -n`, the shell the batch script uses.
func assertBashParses(t *testing.T, script string) {
	t.Helper()
	path, err := exec.LookPath("bash")
	if err != nil {
		t.Log("bash not found, not checking the pre-exec")
		return
	}
	cmd := exec.Command(path, "-n")
	cmd.Stdin = strings.NewReader(script)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	assert.NoError(t, cmd.Run(), "bash -n on the pre-exec: %s", stderr.String())
}

func TestReversePreExec(t *testing.T) {
	rt := &ReverseTunnel{
		Forwards: []ReverseForward{
			{ListenPort: 9000, Host: reverseTestService, Port: 9000},
			{ListenPort: 6379, Host: "redis.other.svc.cluster.local", Port: 6379},
		},
		Bind:       reverseTestBind,
		PythonPath: DefaultSSHReversePythonPath,
	}
	script := reversePreExec(rt, reverseTestNamespace)

	assert.Contains(t, script, "echo '127.77.1.2 minio.user.svc.cluster.local minio.user.svc minio.user minio redis.other.svc.cluster.local redis.other.svc redis.other'",
		"the bare Service name resolves only in the pod's own namespace")
	assert.Contains(t, script, "SINGULARITY_BIND")
	assert.Contains(t, script, "APPTAINER_BIND")
	assert.Contains(t, script, `mktemp "${TMPDIR:-/tmp}/interlink-hosts.XXXXXX"`,
		"the hosts file is node-local and unpredictable, never in the socket directory")
	assert.Contains(t, script, "needs a plugin with SocketDirRoot set")
	assert.Contains(t, script, `python3 -I "$il_rev_d/rev-relay.py" 127.77.1.2 9000="$il_rev_d/rev-9000.sock" 6379="$il_rev_d/rev-6379.sock" &`,
		"the job owns the relay for its whole life")
	assert.Contains(t, script, reverseTestReady)
	assert.Contains(t, script, `[ -S "$il_rev_d/rev-9000.sock" ]`)
	assert.Contains(t, script, `[ -S "$il_rev_d/rev-6379.sock" ]`)
	assert.Contains(t, script, "the reverse relay stopped")
	assert.Contains(t, script, "peer_uid", "the relay is embedded whole")
	assert.NotContains(t, script, "/dev/tcp", "a probe would also succeed against a squatter")
	assert.NotContains(t, script, "trap", "the plugin's EXIT trap owns the directory")
	assert.NotContains(t, script, "il_rev_owner", "no SOCKS wait unless asked")
	assertBashParses(t, script)

	socksOnly := reversePreExec(&ReverseTunnel{SocksPort: 1080, Bind: reverseTestBind, PythonPath: DefaultSSHReversePythonPath}, reverseTestNamespace)
	assert.Contains(t, socksOnly, `until [ "$(il_rev_owner 0438)" = own ]; do`, "wait for the job user's own listener on 1080")
	assert.Contains(t, socksOnly, "another user listens on the SOCKS port")
	assert.NotContains(t, socksOnly, "/etc/hosts", "nothing to name without forwards")
	assert.NotContains(t, socksOnly, "rev-relay.py", "no relay without forwards")
	assertBashParses(t, socksOnly)
}

func TestReverseHostsBind(t *testing.T) {
	path, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not found")
	}
	binds := func(env ...string) string {
		cmd := exec.Command(path, "-c", "il_rev_hosts=/tmp/h\n"+reverseHostsBind+`echo "$SINGULARITY_BIND|$APPTAINER_BIND"`)
		cmd.Env = append([]string{}, env...)
		out, err := cmd.Output()
		require.NoError(t, err)
		return strings.TrimSpace(string(out))
	}

	assert.Equal(t, "/tmp/h:/etc/hosts|/tmp/h:/etc/hosts", binds())
	assert.Equal(t, "/data:/data,/tmp/h:/etc/hosts|/data:/data,/tmp/h:/etc/hosts", binds("SINGULARITY_BIND=/data:/data"),
		"Apptainer ignores SINGULARITY_BIND once APPTAINER_BIND is set, so the job's binds move over")
	assert.Equal(t, "/tmp/h:/etc/hosts|/data:/data,/tmp/h:/etc/hosts", binds("APPTAINER_BIND=/data:/data"))
	assert.Equal(t, "/a:/a,/tmp/h:/etc/hosts|/b:/b,/tmp/h:/etc/hosts", binds("SINGULARITY_BIND=/a:/a", "APPTAINER_BIND=/b:/b"))
}

func TestShouldCreateShadowForReverse(t *testing.T) {
	pod := reversePod(map[string]string{annReverseForwards: reverseTestForward})

	t.Run("a reverse-only pod gets a shadow", func(t *testing.T) {
		p := &Provider{config: normalized(t, reverseConfig())}
		assert.True(t, p.shouldCreateShadow(pod))
		assert.NoError(t, p.checkReverseRequest(pod))
	})

	t.Run("not when the site has not enabled it", func(t *testing.T) {
		p := &Provider{config: normalized(t, sshConfig())}
		assert.False(t, p.shouldCreateShadow(pod))
		err := p.checkReverseRequest(pod)
		assert.Error(t, err, "refused at creation rather than run without its tunnel")
		assert.Contains(t, err.Error(), "Network.SSH.Reverse.Enabled")
	})

	t.Run("not with the wstunnel shadow", func(t *testing.T) {
		config := Config{Network: Network{EnableTunnel: true}}
		assert.NoError(t, NormalizeShadowConfig(&config))
		p := &Provider{config: config}
		assert.False(t, p.shouldCreateShadow(pod))
		assert.Error(t, p.checkReverseRequest(pod))
	})

	t.Run("not with interlink.eu/pod-vpn, which disables the shadow", func(t *testing.T) {
		p := &Provider{config: normalized(t, reverseConfig())}
		vpn := reversePod(map[string]string{annReverseForwards: reverseTestForward, reverseTestPodVPN: "true"})
		assert.False(t, p.shouldCreateShadow(vpn))
		err := p.checkReverseRequest(vpn)
		assert.Error(t, err, "refused at creation rather than run without its tunnel")
		assert.Contains(t, err.Error(), reverseTestPodVPN)
	})

	t.Run("a UDP-only pod gets a shadow only for its reverse forwards", func(t *testing.T) {
		p := &Provider{config: normalized(t, reverseConfig())}
		udp := reversePod(nil)
		udp.Spec.Containers = []v1.Container{{Name: "dns", Ports: []v1.ContainerPort{{ContainerPort: 5353, Protocol: v1.ProtocolUDP}}}}
		assert.False(t, p.shouldCreateShadow(udp), "nothing for ssh to carry")

		udp.Annotations = map[string]string{annReverseForwards: reverseTestForward}
		assert.True(t, p.shouldCreateShadow(udp))
	})

	t.Run("a pod without the annotations is untouched", func(t *testing.T) {
		p := &Provider{config: normalized(t, reverseConfig())}
		plain := reversePod(nil)
		assert.False(t, p.shouldCreateShadow(plain))
		assert.NoError(t, p.checkReverseRequest(plain))
	})
}

func TestCheckReverseRequestSocks(t *testing.T) {
	pod := reversePod(map[string]string{annReverseSocksPort: reverseTestSocks})

	p := &Provider{config: normalized(t, reverseConfig())}
	err := p.checkReverseRequest(pod)
	assert.Error(t, err, "a SOCKS proxy is open to the whole node: off unless the site allows it")
	assert.Contains(t, err.Error(), "Network.SSH.Reverse.AllowSocks")

	config := reverseConfig()
	config.Network.SSH.Reverse.AllowSocks = true
	p = &Provider{config: normalized(t, config)}
	assert.NoError(t, p.checkReverseRequest(pod))
}

func TestNormalizeShadowConfigReverse(t *testing.T) {
	config := normalized(t, reverseConfig())
	r := config.Network.SSH.Reverse
	assert.Equal(t, SSHReverseNodeExecSSH, r.NodeExec)
	assert.Equal(t, SSHReverseSshdNode, r.Sshd)
	assert.Equal(t, DefaultSSHReverseSshdPath, r.SshdPath)
	assert.Equal(t, DefaultSSHReversePythonPath, r.PythonPath)
	assert.Empty(t, config.Network.SSH.ExecConnectCommand, "nothing runs a relay command in portforward mode with ssh")

	config = reverseConfig()
	config.Network.SSH.Reverse.Sshd = " UserSpace "
	config.Network.SSH.Reverse.NodeExec = "SRUN"
	config = normalized(t, config)
	assert.Equal(t, SSHReverseSshdUserspace, config.Network.SSH.Reverse.Sshd)
	assert.Equal(t, SSHReverseNodeExecSrun, config.Network.SSH.Reverse.NodeExec)
	assert.Equal(t, DefaultSSHExecConnectCommand, config.Network.SSH.ExecConnectCommand,
		"srun reaches node:22 with the relay command")

	for field, mutate := range map[string]func(*SSHReverse){
		"Network.SSH.Reverse.NodeExec": func(r *SSHReverse) { r.NodeExec = "telnet" },
		"Network.SSH.Reverse.Sshd":     func(r *SSHReverse) { r.Sshd = "dropbear" },
	} {
		config = reverseConfig()
		mutate(&config.Network.SSH.Reverse)
		err := NormalizeShadowConfig(&config)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), field)
	}
}

func TestUsesNodeHopsWithReverse(t *testing.T) {
	tests := []struct {
		sshd, nodeExec string
		want           bool
	}{
		{SSHReverseSshdNode, SSHReverseNodeExecSSH, true},
		{SSHReverseSshdNode, SSHReverseNodeExecSrun, true},
		{SSHReverseSshdUserspace, SSHReverseNodeExecSSH, true},
		// srun starts the user-space sshd, whose host key the shadow generated.
		{SSHReverseSshdUserspace, SSHReverseNodeExecSrun, false},
	}
	for _, tt := range tests {
		config := reverseConfig()
		config.Network.SSH.Reverse.Sshd = tt.sshd
		config.Network.SSH.Reverse.NodeExec = tt.nodeExec
		config = normalized(t, config)
		assert.Equal(t, tt.want, usesNodeHops(config.Network.SSH), "%s sshd, %s carrier", tt.sshd, tt.nodeExec)
	}

	config := normalized(t, sshConfig())
	assert.False(t, usesNodeHops(config.Network.SSH), "the login target without the reverse tunnel stays off the nodes")
}

// reverseTunnelFor is what parseReverseTunnel yields for a pod with two forwards
// under config.
func reverseTunnelFor(config Config) *ReverseTunnel {
	r := config.Network.SSH.Reverse
	return &ReverseTunnel{
		Forwards:   []ReverseForward{{ListenPort: 9000, Host: reverseTestService, Port: 9000}, {ListenPort: 6443, Host: "kubernetes.default.svc.cluster.local", Port: 443}},
		Bind:       reverseTestBind,
		NodeExec:   r.NodeExec,
		Sshd:       r.Sshd,
		SshdPath:   r.SshdPath,
		PythonPath: r.PythonPath,
		JobName:    reverseTestUID,
	}
}

// renderReverseShadow renders the ssh template for a pod with the given reverse
// tunnel and exposed ports, and returns the manifest, the Deployment and whether a
// Service was rendered. Every container script must parse as shell.
func renderReverseShadow(t *testing.T, config Config, rt *ReverseTunnel, ports []PortMapping) (string, *appsv1.Deployment, bool) {
	t.Helper()
	config = normalized(t, config)
	p := &Provider{config: config}
	manifest, err := p.executeShadowTemplate(t.Context(), ShadowTemplateData{
		Name:               "shadow-trainer-user",
		Namespace:          reverseTestNamespace,
		ExposedPorts:       ports,
		Reverse:            rt,
		NodeConfigMap:      "shadow-trainer-user-node",
		NodeNameKey:        shadowNodeNameKey,
		SocketDirKey:       shadowSocketDirKey,
		NeedsSocketDir:     rt != nil || hasSocketPorts(ports),
		SSH:                config.Network.SSH,
		SSHNodeWaitSeconds: p.sshNodeWaitSeconds(),
	})
	require.NoError(t, err)

	decoder := serializer.NewCodecFactory(scheme.Scheme).UniversalDeserializer()
	var deployment *appsv1.Deployment
	service := false
	for _, doc := range strings.Split(manifest, "---") {
		if strings.TrimSpace(doc) == "" {
			continue
		}
		obj, _, err := decoder.Decode([]byte(doc), nil, nil)
		require.NoError(t, err, "every rendered document must decode:\n%s", doc)
		switch o := obj.(type) {
		case *v1.Service:
			service = true
			assert.NotEmpty(t, o.Spec.Ports, "a Service without ports is rejected by the API server")
		case *appsv1.Deployment:
			deployment = o
		}
	}
	require.NotNil(t, deployment)
	spec := deployment.Spec.Template.Spec
	for _, c := range append(spec.InitContainers, spec.Containers...) {
		assertShellParses(t, c)
	}
	return manifest, deployment, service
}

func containerNames(deployment *appsv1.Deployment) []string {
	var names []string
	for _, c := range deployment.Spec.Template.Spec.Containers {
		names = append(names, c.Name)
	}
	return names
}

// reverseScript returns the script of the ssh-reverse container.
// TestReverseHoldEndsWithItsSockets runs build_rflags from the rendered script: a
// requeue on the same node recreates the socket directory under the same path, so
// the session holding the forwards must end once their sockets are gone.
func TestReverseHoldEndsWithItsSockets(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not found")
	}
	config := normalized(t, withReverse(sshConfig(), SSHReverseSshdNode, SSHReverseNodeExecSSH))
	_, deployment, _ := renderReverseShadow(t, config, reverseTunnelFor(config), nil)
	script := reverseScript(t, deployment)
	start := strings.Index(script, "build_rflags() {\n")
	require.GreaterOrEqual(t, start, 0)
	end := strings.Index(script[start:], "\n}\n")
	require.Positive(t, end)
	buildRflags := script[start : start+end+3]

	dir, err := os.MkdirTemp("", "rh")
	require.NoError(t, err)
	defer os.RemoveAll(dir)
	for _, port := range []string{"9000", "6379"} {
		l, err := net.Listen("unix", filepath.Join(dir, "rev-"+port+".sock"))
		require.NoError(t, err)
		l.(*net.UnixListener).SetUnlinkOnClose(false)
		defer l.Close()
	}
	held := func() (bool, bool) {
		script := buildRflags + `build_rflags
# the loop condition of each hold command
c=${hold#while } && eval "${c%%; do*}" && echo hold
c=${hold_dir#while } && eval "${c%%; do*}" && echo dir
exit 0`
		cmd := exec.Command(sh, "-c", script) // #nosec G204 -- the script this test rendered
		cmd.Env = []string{"REV_FORWARDS=9000:minio.user.svc:9000 6379:redis.user.svc:6379", "sd=" + dir}
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s", out)
		return strings.Contains(string(out), "hold"), strings.Contains(string(out), "dir")
	}

	forwards, socks := held()
	assert.True(t, forwards)
	assert.True(t, socks)

	require.NoError(t, os.Remove(filepath.Join(dir, "rev-6379.sock")))
	forwards, socks = held()
	assert.False(t, forwards, "a forward whose socket is gone must end its session")
	assert.True(t, socks, "the SOCKS session holds no socket in the directory")
}

func reverseScript(t *testing.T, deployment *appsv1.Deployment) string {
	t.Helper()
	for _, c := range deployment.Spec.Template.Spec.Containers {
		if c.Name == reverseTestContainer && len(c.Args) > 0 {
			return c.Args[0]
		}
	}
	t.Fatal("no ssh-reverse container")
	return ""
}

func withReverse(config Config, sshd, nodeExec string) Config {
	config.Network.SSH.Reverse.Enabled = true
	config.Network.SSH.Reverse.Sshd = sshd
	config.Network.SSH.Reverse.NodeExec = nodeExec
	return config
}

func TestSSHShadowTemplateReverse(t *testing.T) {
	config := normalized(t, reverseConfig())
	rt := reverseTunnelFor(config)
	tcp := []PortMapping{{Port: 8888, Name: "jupyter", Protocol: DefaultProtocol}}

	t.Run("forwards and reverse side by side", func(t *testing.T) {
		manifest, deployment, service := renderReverseShadow(t, config, rt, tcp)
		assert.Contains(t, containerNames(deployment), "ssh-forward")
		assert.Contains(t, containerNames(deployment), reverseTestContainer)
		assert.True(t, service)
		assert.Contains(t, manifest, `value: "9000:minio.user.svc.cluster.local:9000 6443:kubernetes.default.svc.cluster.local:443"`)
		assert.Contains(t, manifest, `-L "0.0.0.0:8888:$node:8888"`)
		assert.Contains(t, manifest, `name: REV_SOCKS_PORT
          value: ""`, "no SOCKS listener unless the pod asked")
	})

	t.Run("a reverse-only pod renders no forward and no Service, and waits for the socket directory", func(t *testing.T) {
		_, deployment, service := renderReverseShadow(t, config, rt, nil)
		assert.NotContains(t, containerNames(deployment), "ssh-forward")
		assert.Contains(t, containerNames(deployment), reverseTestContainer)
		assert.False(t, service)
		assert.Contains(t, initScript(t, deployment), "/interlink/node/"+shadowSocketDirKey)
	})

	t.Run("a forward-only pod is unchanged", func(t *testing.T) {
		manifest, deployment, service := renderReverseShadow(t, config, nil, tcp)
		assert.Contains(t, containerNames(deployment), "ssh-forward")
		assert.NotContains(t, containerNames(deployment), reverseTestContainer)
		assert.True(t, service)
		assert.NotContains(t, manifest, reverseTestUserspace)
		assert.NotContains(t, manifest, "job_id")
	})

	t.Run("node sshd forwards to the job's sockets and holds while the job runs", func(t *testing.T) {
		manifest, deployment, _ := renderReverseShadow(t, config, rt, nil)
		script := reverseScript(t, deployment)

		for _, gone := range []string{reverseTestUserspace, "ssh-keygen -q", "ForceCommand", "EORELAY", "relay.py", "/tmp/il-rev-"} {
			assert.NotContains(t, script, gone)
		}
		assert.NotContains(t, manifest, "REV_OWNER_ONLY")
		assert.NotContains(t, manifest, "REV_SSHD", "the node's own sshd needs no path")
		assert.Contains(t, script, `"rm -f $sd/rev-*.sock"`, "a stale socket would fail the -R")
		assert.Contains(t, script, `rflags="$rflags -R $sd/rev-$lport.sock:$dest"`)
		assert.Contains(t, script, `hold="while $held; do sleep 10; echo; done"`,
			"the hold loop must write to its session, or it outlives a dropped one")
		assert.Contains(t, script, `node_hop "$REV_NODE_EXEC"`)
		assert.Contains(t, script, `-o ExitOnForwardFailure=yes $node_opts "$@" "alice@$node" "$h" >/dev/null &`)
		assert.Contains(t, script, `session "$hold" $rflags`+"\n")
		assert.Contains(t, script, "retrying in 30s")
		assert.NotContains(t, script, testRestartHint)
		assert.NotContains(t, script, "StrictHostKeyChecking=accept-new $node", "no unchecked hop from the login node")
	})

	t.Run("userspace sshd goes through the checked node hop", func(t *testing.T) {
		userspace := normalized(t, withReverse(sshConfig(), SSHReverseSshdUserspace, SSHReverseNodeExecSSH))
		manifest, deployment, _ := renderReverseShadow(t, userspace, reverseTunnelFor(userspace), nil)
		script := reverseScript(t, deployment)

		assert.Contains(t, script, reverseTestUserspace)
		assert.Contains(t, script, "node_hop ssh || return 1")
		assert.Contains(t, script, `run="ssh $node_opts alice@$node"`)
		assert.Contains(t, optionSet(t, script, "node_opts"), "ProxyCommand=$w/node-proxy")
		assert.Contains(t, script, "cd $sd || exit 1")
		assert.Contains(t, script, "StreamLocalBindMask 0177")
		assert.NotContains(t, script, "GatewayPorts")
		assert.NotContains(t, script, "ForceCommand")
		assert.NotContains(t, script, "rm -rf $sd", "the job owns its directory")
		assert.Contains(t, script, `"$@" alice@interlink-job "$h" >/dev/null &`)
		assert.Contains(t, manifest, `name: REV_SSHD
          value: "/usr/sbin/sshd"`)
	})

	t.Run("srun reaches node:22 inside the allocation", func(t *testing.T) {
		srun := normalized(t, withReverse(sshConfig(), SSHReverseSshdNode, SSHReverseNodeExecSrun))
		_, deployment, _ := renderReverseShadow(t, srun, reverseTunnelFor(srun), nil)
		script := reverseScript(t, deployment)

		assert.Contains(t, script, `exec ssh %s %s "srun --jobid=%s --overlap -u -n1 -w %s --cpus-per-task=1 %s 127.0.0.1 22"`,
			"pinned to the batch host, where the relay runs")
		assert.Contains(t, script, `squeue -h --me -t R -n $REV_JOB_NAME -o '%i %B'`)
		assert.Contains(t, script, "connect_cmd='nc'")
	})

	t.Run("socks warns on every start", func(t *testing.T) {
		socks := *rt
		socks.SocksPort = 1080
		manifest, deployment, _ := renderReverseShadow(t, config, &socks, nil)
		assert.Contains(t, manifest, `name: REV_SOCKS_PORT
          value: "1080"`)
		script := reverseScript(t, deployment)
		assert.Contains(t, script, `session "$hold_dir" -R "127.0.0.1:$REV_SOCKS_PORT"`,
			"the SOCKS listener is not in the socket directory")
		assert.NotContains(t, script, `rflags="$rflags -R 127.0.0.1`,
			"a port another user takes must not fail the session holding the forwards")
		assert.Contains(t, script, "keep_socks\n")
		assert.Contains(t, script, "WARNING: SOCKS proxy on 127.0.0.1:$REV_SOCKS_PORT")
	})

	t.Run("kerberos delegates on the login hop only for srun", func(t *testing.T) {
		for nodeExec, login := range map[string]string{SSHReverseNodeExecSrun: "yes", SSHReverseNodeExecSSH: "no"} {
			krb := normalized(t, withReverse(kerberosConfig(), SSHReverseSshdNode, nodeExec))
			_, deployment, _ := renderReverseShadow(t, krb, reverseTunnelFor(krb), nil)
			script := reverseScript(t, deployment)

			assert.Contains(t, optionSet(t, script, "ssh_opts"), "-o GSSAPIDelegateCredentials="+login, nodeExec)
			assert.Contains(t, optionSet(t, script, "node_opts"), "-o GSSAPIDelegateCredentials=no", nodeExec)
			assert.NotContains(t, optionSet(t, script, "node_opts"), "GSSAPIDelegateCredentials=yes", nodeExec)
		}
	})
}

// TestSSHShadowReverseScriptsParse renders the reverse container for every sshd,
// carrier, forward mode and auth method; renderReverseShadow runs each container
// script through the shell parser.
func TestSSHShadowReverseScriptsParse(t *testing.T) {
	for _, sshd := range []string{SSHReverseSshdNode, SSHReverseSshdUserspace} {
		for _, nodeExec := range []string{SSHReverseNodeExecSSH, SSHReverseNodeExecSrun} {
			for _, mode := range []string{SSHForwardModePortForward, SSHForwardModeExec} {
				for _, auth := range []string{SSHAuthPublicKey, SSHAuthKerberos} {
					t.Run(strings.Join([]string{sshd, nodeExec, mode, auth}, "/"), func(t *testing.T) {
						config := sshConfig()
						if auth == SSHAuthKerberos {
							config = kerberosConfig()
						}
						config = withForward(config, SSHForwardTargetNode, mode)
						config = normalized(t, withReverse(config, sshd, nodeExec))
						rt := reverseTunnelFor(config)
						rt.SocksPort = 1080
						renderReverseShadow(t, config, rt, socketAndTCPPorts())
					})
				}
			}
		}
	}
}

// TestCreateShadowPodRecordsSocksEvent runs createShadowPod against a fake API
// server and checks that a SOCKS proxy, and only a SOCKS proxy, leaves a Warning
// Event on the pod.
func TestCreateShadowPodRecordsSocksEvent(t *testing.T) {
	config := reverseConfig()
	config.Network.SSH.Reverse.AllowSocks = true
	config = normalized(t, config)

	for _, socks := range []bool{true, false} {
		annotations := map[string]string{annReverseForwards: reverseTestService + ":9000"}
		if socks {
			annotations[annReverseSocksPort] = reverseTestSocks
		}
		pod := reversePod(annotations)
		identity, err := computeShadowResourceIdentity(pod)
		require.NoError(t, err)
		key := &v1.Secret{ObjectMeta: metav1.ObjectMeta{Name: testSSHKeySecret, Namespace: config.Namespace}}
		running := &v1.Pod{ObjectMeta: metav1.ObjectMeta{
			Name:      identity.Name + "-0",
			Namespace: identity.Namespace,
			Labels:    map[string]string{shadowComponentLabel: identity.Name},
		}}
		recorder := record.NewFakeRecorder(10)
		p := &Provider{clientSet: fake.NewSimpleClientset(key, running), config: config}
		p.SetEventRecorder(recorder)

		_, data, err := p.createShadowPod(t.Context(), pod)
		require.NoError(t, err)
		assert.True(t, data.NeedsSocketDir, "the reverse tunnel lives in the socket directory")

		select {
		case event := <-recorder.Events:
			assert.True(t, socks, "unexpected event %q", event)
			assert.Contains(t, event, "Warning ReverseSocksProxy SOCKS proxy on 127.0.0.1:1080")
		default:
			assert.False(t, socks, "no ReverseSocksProxy event")
		}
	}
}

// startRelay writes the embedded relay into dir and runs it with python3 -I,
// skipping where python3 or /proc/net/tcp is missing.
func startRelay(t *testing.T, dir string, args ...string) (*exec.Cmd, *bytes.Buffer) {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not found")
	}
	if _, err := os.Stat("/proc/net/tcp"); err != nil {
		t.Skip("no /proc/net/tcp")
	}
	script := filepath.Join(dir, "rev-relay.py")
	require.NoError(t, os.WriteFile(script, []byte(reverseRelay), 0o600))
	cmd := exec.Command(python, append([]string{"-I", script}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			t.Logf("stopping the relay: %v", err)
		}
	})
	return cmd, &stderr
}

// waitExit waits up to timeout for cmd and returns its exit code, or -1.
func waitExit(cmd *exec.Cmd, timeout time.Duration) int {
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode()
		}
		if err == nil {
			return 0
		}
		return -1
	case <-time.After(timeout):
		return -1
	}
}

func freeLoopbackPort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
	require.NoError(t, l.Close())
	return port
}

func TestReverseRelay(t *testing.T) {
	t.Run("refuses to start when its address is taken", func(t *testing.T) {
		squatter, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		defer squatter.Close()
		port := strconv.Itoa(squatter.Addr().(*net.TCPAddr).Port)
		dir := t.TempDir()

		cmd, stderr := startRelay(t, dir, "127.0.0.1", port+"="+filepath.Join(dir, "rev-"+port+".sock"))

		assert.Equal(t, 1, waitExit(cmd, 15*time.Second), "a squatter must fail the job, not share the port")
		assert.Contains(t, stderr.String(), "relay: cannot listen on 127.0.0.1:"+port)
		assert.NoFileExists(t, filepath.Join(dir, reverseTestReady))
	})

	t.Run("signals ready, relays to the socket and exits with its directory", func(t *testing.T) {
		port := freeLoopbackPort(t)
		dir, err := os.MkdirTemp("", "rr")
		require.NoError(t, err)
		defer os.RemoveAll(dir)
		sock := filepath.Join(dir, "rev-"+port+".sock")
		upstream, err := net.Listen("unix", sock)
		if err != nil {
			t.Skipf("cannot listen on %s: %v", sock, err)
		}
		defer upstream.Close()
		go func() {
			conn, err := upstream.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
			buf := make([]byte, 4)
			if _, err := io.ReadFull(conn, buf); err != nil {
				return
			}
			if _, err := conn.Write(bytes.ToUpper(buf)); err != nil {
				return
			}
		}()

		cmd, stderr := startRelay(t, dir, "127.0.0.1", port+"="+sock)
		require.Eventually(t, func() bool {
			_, err := os.Stat(filepath.Join(dir, reverseTestReady))
			return err == nil
		}, 15*time.Second, 100*time.Millisecond, "no ready file: %s", stderr)

		conn, err := net.Dial("tcp", "127.0.0.1:"+port)
		require.NoError(t, err)
		_, err = conn.Write([]byte("ping"))
		require.NoError(t, err)
		reply := make([]byte, 4)
		require.NoError(t, conn.SetReadDeadline(time.Now().Add(10*time.Second)))
		_, err = io.ReadFull(conn, reply)
		require.NoError(t, err, "the job user's own connection must pass: %s", stderr)
		assert.Equal(t, "PING", string(reply))
		conn.Close()

		require.NoError(t, os.RemoveAll(dir))
		assert.Equal(t, 0, waitExit(cmd, 15*time.Second), "the relay must not outlive the job's directory")
	})

	// A client on an IPv6 socket, as a JVM opens by default, reaches the IPv4
	// listener through a v4-mapped address and is listed in /proc/net/tcp6.
	t.Run("accepts the job user's own IPv6 socket on a v4-mapped address", func(t *testing.T) {
		if _, err := os.Stat("/proc/net/tcp6"); err != nil {
			t.Skip("no /proc/net/tcp6")
		}
		port := freeLoopbackPort(t)
		dir, err := os.MkdirTemp("", "rr")
		require.NoError(t, err)
		defer os.RemoveAll(dir)
		sock := filepath.Join(dir, "rev-"+port+".sock")
		upstream, err := net.Listen("unix", sock)
		if err != nil {
			t.Skipf("cannot listen on %s: %v", sock, err)
		}
		defer upstream.Close()
		go func() {
			conn, err := upstream.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
			if _, err := io.Copy(conn, conn); err != nil {
				return
			}
		}()

		cmd, stderr := startRelay(t, dir, "127.0.0.1", port+"="+sock)
		require.Eventually(t, func() bool {
			_, err := os.Stat(filepath.Join(dir, reverseTestReady))
			return err == nil
		}, 15*time.Second, 100*time.Millisecond, "no ready file: %s", stderr)

		// Go dials a v4-mapped address on an IPv4 socket, so the client is python's
		probe := `import socket, sys
s = socket.socket(socket.AF_INET6)
s.settimeout(10)
s.connect(("::ffff:127.0.0.1", int(sys.argv[1])))
s.sendall(b"ping")
print(s.recv(4).decode())`
		client := exec.Command(cmd.Path, "-I", "-c", probe, port) // #nosec G204 -- python3 found by startRelay
		out, err := client.CombinedOutput()
		require.NoError(t, err, "%s", out)
		assert.Equal(t, "ping", strings.TrimSpace(string(out)), "relay log: %s", stderr)
	})

	t.Run("keeps serving while the tunnel is down and logs that once", func(t *testing.T) {
		port := freeLoopbackPort(t)
		dir, err := os.MkdirTemp("", "rr")
		require.NoError(t, err)
		defer os.RemoveAll(dir)
		sock := filepath.Join(dir, "rev-"+port+".sock")

		cmd, stderr := startRelay(t, dir, "127.0.0.1", port+"="+sock)
		require.Eventually(t, func() bool {
			_, err := os.Stat(filepath.Join(dir, reverseTestReady))
			return err == nil
		}, 15*time.Second, 100*time.Millisecond, "no ready file")

		for range 3 {
			conn, err := net.Dial("tcp", "127.0.0.1:"+port)
			require.NoError(t, err)
			require.NoError(t, conn.SetReadDeadline(time.Now().Add(10*time.Second)))
			_, err = conn.Read(make([]byte, 1))
			assert.ErrorIs(t, err, io.EOF, "a connection with no tunnel behind it is closed at once")
			conn.Close()
		}

		require.NoError(t, os.RemoveAll(dir))
		require.Equal(t, 0, waitExit(cmd, 15*time.Second))
		assert.Equal(t, 1, strings.Count(stderr.String(), sock+":"),
			"the log is the job's output: one line per outage, not per connection:\n%s", stderr)
	})
}
