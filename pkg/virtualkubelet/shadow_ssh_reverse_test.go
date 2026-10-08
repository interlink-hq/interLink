package virtualkubelet

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/kubernetes/scheme"
)

const (
	reverseTestNamespace = "user"
	reverseTestService   = "minio.user.svc.cluster.local"
	reverseTestBind      = "127.77.1.2"
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
		UID:         "0d9b2d4a-6f6e-4a5f-9a66-2c1c5a4e9f10",
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
		assert.True(t, rt.OwnerOnly)
		assert.Equal(t, SSHReverseNodeExecSSH, rt.NodeExec)
		assert.Equal(t, DefaultSSHReverseSshdPath, rt.SshdPath)
		assert.Equal(t, string(pod.UID), rt.JobName)
		assert.Equal(t, reverseBindAddress(string(pod.UID)), rt.Bind)
	})

	t.Run("remapped listen port, socks and overrides", func(t *testing.T) {
		pod := reversePod(map[string]string{
			annReverseForwards:  "6443:kubernetes.default.svc.cluster.local:443 redis.user.svc:6379",
			annReverseSocksPort: "1080",
			annReverseBind:      reverseTestBind,
			annReverseNodeExec:  "srun",
			annReverseOwnerOnly: "false",
		})
		rt, err := parseReverseTunnel(pod, cfg)
		assert.NoError(t, err)
		assert.Equal(t, []ReverseForward{
			{ListenPort: 6443, Host: "kubernetes.default.svc.cluster.local", Port: 443},
			{ListenPort: 6379, Host: "redis.user.svc", Port: 6379},
		}, rt.Forwards)
		assert.Equal(t, int32(1080), rt.SocksPort)
		assert.Equal(t, reverseTestBind, rt.Bind)
		assert.Equal(t, SSHReverseNodeExecSrun, rt.NodeExec)
		assert.False(t, rt.OwnerOnly)
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
		{"bind outside loopback", map[string]string{annReverseSocksPort: "1080", annReverseBind: "10.0.0.1"}, "127.x.y.z"},
		{"bind on the shared loopback address", map[string]string{annReverseSocksPort: "1080", annReverseBind: "127.0.0.1"}, "127.x.y.z"},
		{"unknown node exec", map[string]string{annReverseSocksPort: "1080", annReverseNodeExec: "rsh"}, "not \"ssh\" or \"srun\""},
		{"owner-only that is not a bool", map[string]string{annReverseSocksPort: "1080", annReverseOwnerOnly: "maybe"}, "not true or false"},
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
	a := reverseBindAddress("0d9b2d4a-6f6e-4a5f-9a66-2c1c5a4e9f10")
	b := reverseBindAddress("6c0f1e3a-1b2c-4d5e-8f90-a1b2c3d4e5f6")
	assert.Equal(t, a, reverseBindAddress("0d9b2d4a-6f6e-4a5f-9a66-2c1c5a4e9f10"), "the address must not change across restarts")
	assert.NotEqual(t, a, b, "two pods on one node need distinct addresses")
	for _, addr := range []string{a, b} {
		assert.Regexp(t, `^127\.([1-9]|[1-9][0-9]|1[0-9]{2}|2[0-4][0-9]|25[0-4])\.[0-9]{1,3}\.([1-9]|[1-9][0-9]|1[0-9]{2}|2[0-4][0-9]|25[0-4])$`, addr,
			"stay out of 127.0.0.0/24 and off .0 and .255")
	}
}

func TestReversePreExec(t *testing.T) {
	rt := &ReverseTunnel{
		Forwards: []ReverseForward{
			{ListenPort: 9000, Host: reverseTestService, Port: 9000},
			{ListenPort: 6379, Host: "redis.other.svc.cluster.local", Port: 6379},
		},
		Bind: reverseTestBind,
	}
	script := reversePreExec(rt, reverseTestNamespace)

	assert.Contains(t, script, "echo '127.77.1.2 minio.user.svc.cluster.local minio.user.svc minio.user minio redis.other.svc.cluster.local redis.other.svc redis.other'",
		"the bare Service name resolves only in the pod's own namespace")
	assert.Contains(t, script, "/dev/tcp/127.77.1.2/9000", "wait on the first forward")
	assert.Contains(t, script, "SINGULARITY_BIND")
	assert.Contains(t, script, "APPTAINER_BIND")

	socksOnly := reversePreExec(&ReverseTunnel{SocksPort: 1080, Bind: reverseTestBind}, reverseTestNamespace)
	assert.NotContains(t, socksOnly, "/etc/hosts", "nothing to name without forwards")
	assert.Contains(t, socksOnly, "/dev/tcp/127.77.1.2/1080", "wait on the SOCKS port instead")
}

func TestShouldCreateShadowForReverse(t *testing.T) {
	pod := reversePod(map[string]string{annReverseForwards: "minio.user.svc:9000"})

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

	t.Run("a pod without the annotations is untouched", func(t *testing.T) {
		p := &Provider{config: normalized(t, reverseConfig())}
		plain := reversePod(nil)
		assert.False(t, p.shouldCreateShadow(plain))
		assert.NoError(t, p.checkReverseRequest(plain))
	})
}

func TestNormalizeShadowConfigReverse(t *testing.T) {
	config := normalized(t, reverseConfig())
	assert.Equal(t, SSHReverseNodeExecSSH, config.Network.SSH.Reverse.NodeExec)
	assert.Equal(t, DefaultSSHReverseSshdPath, config.Network.SSH.Reverse.SshdPath)
	assert.Equal(t, DefaultSSHReversePythonPath, config.Network.SSH.Reverse.PythonPath)

	config = reverseConfig()
	config.Network.SSH.Reverse.NodeExec = "telnet"
	err := NormalizeShadowConfig(&config)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Network.SSH.Reverse.NodeExec")
}

// renderReverseShadow renders the ssh template for a pod with the given reverse
// settings and exposed ports, returning the manifest and the container names.
func renderReverseShadow(t *testing.T, rt *ReverseTunnel, ports []PortMapping) (string, []string, bool) {
	t.Helper()
	config := normalized(t, reverseConfig())
	p := &Provider{config: config}
	manifest, err := p.executeShadowTemplate(t.Context(), ShadowTemplateData{
		Name:               "shadow-trainer-user",
		Namespace:          reverseTestNamespace,
		ExposedPorts:       ports,
		Reverse:            rt,
		NodeConfigMap:      "shadow-trainer-user-node",
		NodeNameKey:        shadowNodeNameKey,
		SSH:                config.Network.SSH,
		SSHNodeWaitSeconds: p.sshNodeWaitSeconds(),
	})
	assert.NoError(t, err)

	decoder := serializer.NewCodecFactory(scheme.Scheme).UniversalDeserializer()
	var containers []string
	service := false
	for _, doc := range strings.Split(manifest, "---") {
		if strings.TrimSpace(doc) == "" {
			continue
		}
		obj, _, err := decoder.Decode([]byte(doc), nil, nil)
		assert.NoError(t, err, "every rendered document must decode:\n%s", doc)
		if o, ok := obj.(*v1.Service); ok {
			service = true
			assert.NotEmpty(t, o.Spec.Ports, "a Service without ports is rejected by the API server")
		}
		if strings.Contains(doc, "kind: Deployment") {
			for _, line := range strings.Split(doc, "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "- name: ") && !strings.Contains(line, "shadow-") && !strings.HasPrefix(line, "- name: REV_") && !strings.HasPrefix(line, "- name: KRB5") {
					containers = append(containers, strings.TrimPrefix(line, "- name: "))
				}
			}
		}
	}
	return manifest, containers, service
}

func TestSSHShadowTemplateReverse(t *testing.T) {
	rt := &ReverseTunnel{
		Forwards:   []ReverseForward{{ListenPort: 9000, Host: reverseTestService, Port: 9000}, {ListenPort: 6443, Host: "kubernetes.default.svc.cluster.local", Port: 443}},
		Bind:       reverseTestBind,
		NodeExec:   SSHReverseNodeExecSSH,
		OwnerOnly:  true,
		SshdPath:   DefaultSSHReverseSshdPath,
		PythonPath: DefaultSSHReversePythonPath,
		JobName:    "0d9b2d4a-6f6e-4a5f-9a66-2c1c5a4e9f10",
	}
	tcp := []PortMapping{{Port: 8888, Name: "jupyter", Protocol: DefaultProtocol}}

	t.Run("forwards and reverse side by side", func(t *testing.T) {
		manifest, containers, service := renderReverseShadow(t, rt, tcp)
		assert.Contains(t, containers, "ssh-forward")
		assert.Contains(t, containers, "ssh-reverse")
		assert.True(t, service)
		assert.Contains(t, manifest, `value: "9000:minio.user.svc.cluster.local:9000 6443:kubernetes.default.svc.cluster.local:443"`)
		assert.Contains(t, manifest, `-L "0.0.0.0:8888:$node:8888"`)
		assert.Contains(t, manifest, `name: REV_SOCKS_PORT
          value: ""`, "no SOCKS listener unless the pod asked")
	})

	t.Run("a reverse-only pod renders no forward and no Service", func(t *testing.T) {
		_, containers, service := renderReverseShadow(t, rt, nil)
		assert.NotContains(t, containers, "ssh-forward")
		assert.Contains(t, containers, "ssh-reverse")
		assert.False(t, service)
	})

	t.Run("a forward-only pod is unchanged", func(t *testing.T) {
		manifest, containers, service := renderReverseShadow(t, nil, tcp)
		assert.Contains(t, containers, "ssh-forward")
		assert.NotContains(t, containers, "ssh-reverse")
		assert.True(t, service)
		assert.NotContains(t, manifest, "sshd -i")
	})

	t.Run("socks and srun carry through", func(t *testing.T) {
		socks := *rt
		socks.SocksPort = 1080
		socks.NodeExec = SSHReverseNodeExecSrun
		manifest, _, _ := renderReverseShadow(t, &socks, nil)
		assert.Contains(t, manifest, `name: REV_SOCKS_PORT
          value: "1080"`)
		assert.Contains(t, manifest, `name: REV_NODE_EXEC
          value: "srun"`)
		assert.Contains(t, manifest, "squeue -h --me -t R -n $REV_JOB_NAME", "srun mode finds the allocation by the job name")
	})
}
