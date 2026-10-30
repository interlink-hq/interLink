package virtualkubelet

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net"
	"regexp"
	"strconv"
	"strings"

	"github.com/containerd/containerd/log"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
)

// Pod annotations asking the ssh shadow for connections back into the cluster.
const (
	// annReverseForwards lists the cluster endpoints the pod may open connections
	// to, space separated, each "host:port" or "listen-port:host:port".
	annReverseForwards = "interlink.eu/reverse-forwards"
	// annReverseSocksPort asks for a SOCKS5 proxy on the given port for endpoints
	// not listed in annReverseForwards.
	annReverseSocksPort = "interlink.eu/reverse-socks-port"
	// annReverseBind overrides the 127.x.y.z address of the owner-only relay
	// inside the job, normally derived from the pod UID.
	annReverseBind = "interlink.eu/reverse-bind"
)

// How the shadow reaches the compute node.
const (
	// SSHReverseNodeExecSSH opens node:22 through the login node, as
	// Network.SSH.ForwardMode says. Needs the site to allow ssh into a node where
	// the user has a running job.
	SSHReverseNodeExecSSH = "ssh"
	// SSHReverseNodeExecSrun runs `srun --jobid=<job> --overlap` on the login node,
	// inside the job's own allocation. A last resort: lower throughput, and the
	// login node needs the user's Kerberos ticket where there is one.
	SSHReverseNodeExecSrun = "srun"
)

// Which sshd makes the forwards on the compute node.
const (
	// SSHReverseSshdNode is the compute node's own sshd.
	SSHReverseSshdNode = "node"
	// SSHReverseSshdUserspace is an sshd the shadow starts in the job, as the
	// job's user, with its own config, for sites whose node sshd refuses
	// forwarding.
	SSHReverseSshdUserspace = "userspace"
)

const (
	// DefaultSSHReverseSshdPath is where sshd is looked for on the compute node.
	DefaultSSHReverseSshdPath = "/usr/sbin/sshd"
	// DefaultSSHReversePythonPath runs the job's owner-only relay.
	DefaultSSHReversePythonPath = "python3"
	// reverseTunnelWaitSeconds bounds how long the job waits for its tunnel before
	// giving up. The tunnel follows the compute node name, which the shadow learns a
	// status poll or two after the job starts.
	reverseTunnelWaitSeconds = 600
	// reversePrivilegedPortLimit: an unprivileged sshd cannot bind below it.
	reversePrivilegedPortLimit = 1024
)

// ReverseForward is one cluster endpoint the job may connect to, as the shadow
// template sees it: the job dials ListenPort on its loopback address, the shadow
// dials Host:Port.
type ReverseForward struct {
	ListenPort int32
	Host       string
	Port       int32
}

// ReverseTunnel carries the pod's reverse settings into the ssh shadow template.
// Everything in it has been validated: the template passes it to ssh unquoted.
type ReverseTunnel struct {
	Forwards []ReverseForward
	// SocksPort is 0 when no SOCKS proxy was asked for.
	SocksPort int32
	// Bind is the 127.x.y.z address the job's relay listens on.
	Bind string
	// NodeExec is SSHReverseNodeExecSSH or SSHReverseNodeExecSrun.
	NodeExec string
	// Sshd is SSHReverseSshdNode or SSHReverseSshdUserspace.
	Sshd       string
	SshdPath   string
	PythonPath string
	// JobName is how srun mode finds the allocation: the plugin names the batch
	// job after the pod UID.
	JobName string
}

var reverseHostPattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$`)

// hasReverseAnnotations reports whether the pod asked for any reverse forward.
func hasReverseAnnotations(pod *v1.Pod) bool {
	if pod == nil || pod.Annotations == nil {
		return false
	}
	return strings.TrimSpace(pod.Annotations[annReverseForwards]) != "" ||
		strings.TrimSpace(pod.Annotations[annReverseSocksPort]) != ""
}

// reverseRequested reports whether the pod gets a reverse tunnel: it asked for
// one and this deployment can provide it.
func (p *Provider) reverseRequested(pod *v1.Pod) bool {
	return p.isSSHShadow() && p.config.Network.SSH.Reverse.Enabled && hasReverseAnnotations(pod)
}

// checkReverseRequest refuses, at creation, a pod that asks for the reverse
// tunnel on a deployment that cannot provide it. Silently running it without the
// tunnel would only move the failure to the first connection the job opens.
func (p *Provider) checkReverseRequest(pod *v1.Pod) error {
	if !hasReverseAnnotations(pod) {
		return nil
	}
	switch {
	case !p.isSSHShadow():
		return fmt.Errorf("pod %s/%s sets %s, which needs Network.ShadowMode %q", pod.Namespace, pod.Name, annReverseForwards, ShadowModeSSH)
	case !p.config.Network.EnableTunnel:
		return fmt.Errorf("pod %s/%s sets %s, but Network.EnableTunnel is false", pod.Namespace, pod.Name, annReverseForwards)
	case !p.config.Network.SSH.Reverse.Enabled:
		return fmt.Errorf("pod %s/%s sets %s, but this virtual kubelet does not allow it (Network.SSH.Reverse.Enabled)", pod.Namespace, pod.Name, annReverseForwards)
	case pod.Annotations["interlink.eu/pod-vpn"] != "":
		return fmt.Errorf("pod %s/%s sets %s, but interlink.eu/pod-vpn disables its shadow tunnel", pod.Namespace, pod.Name, annReverseForwards)
	case strings.TrimSpace(pod.Annotations[annReverseSocksPort]) != "" && !p.config.Network.SSH.Reverse.AllowSocks:
		return fmt.Errorf("pod %s/%s sets %s, but this virtual kubelet does not allow a SOCKS proxy (Network.SSH.Reverse.AllowSocks)", pod.Namespace, pod.Name, annReverseSocksPort)
	}
	return nil
}

// warnSocksProxy tells the pod's owner, in the VK log and as an Event on the pod,
// that its SOCKS proxy is open to every user of the compute node.
func (p *Provider) warnSocksProxy(ctx context.Context, pod *v1.Pod, port int32) {
	msg := fmt.Sprintf("SOCKS proxy on 127.0.0.1:%d on the compute node: every user of that node can reach through it any endpoint the shadow reaches in the cluster", port)
	log.G(ctx).Warningf("pod %s/%s: %s", pod.Namespace, pod.Name, msg)
	if p.eventRecorder != nil {
		p.eventRecorder.Event(pod, v1.EventTypeWarning, "ReverseSocksProxy", msg)
	}
}

// parseReverseTunnel turns the pod's reverse annotations into template data,
// rejecting anything that could not work or that the template could not pass to
// ssh safely.
func parseReverseTunnel(pod *v1.Pod, cfg SSHReverse) (*ReverseTunnel, error) {
	ann := pod.Annotations
	rt := &ReverseTunnel{
		NodeExec:   cfg.NodeExec,
		Sshd:       cfg.Sshd,
		SshdPath:   cfg.SshdPath,
		PythonPath: cfg.PythonPath,
		JobName:    string(pod.UID),
		Bind:       reverseBindAddress(string(pod.UID)),
	}

	listening := map[int32]string{}
	for _, spec := range strings.Fields(ann[annReverseForwards]) {
		f, err := parseReverseForward(spec)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", annReverseForwards, err)
		}
		if other, taken := listening[f.ListenPort]; taken {
			return nil, fmt.Errorf("%s: %q and %q both listen on port %d", annReverseForwards, other, spec, f.ListenPort)
		}
		listening[f.ListenPort] = spec
		rt.Forwards = append(rt.Forwards, f)
	}

	if v := strings.TrimSpace(ann[annReverseSocksPort]); v != "" {
		port, err := parseReversePort(v)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", annReverseSocksPort, err)
		}
		if port < reversePrivilegedPortLimit {
			return nil, fmt.Errorf("%s: port %d is below %d, which an unprivileged sshd cannot bind on the compute node", annReverseSocksPort, port, reversePrivilegedPortLimit)
		}
		if other, taken := listening[port]; taken {
			return nil, fmt.Errorf("%s: port %d is already used by forward %q", annReverseSocksPort, port, other)
		}
		rt.SocksPort = port
	}

	if v := strings.TrimSpace(ann[annReverseBind]); v != "" {
		ip := net.ParseIP(v).To4()
		if ip == nil || ip[0] != 127 || v == "127.0.0.1" {
			return nil, fmt.Errorf("%s: %q is not a 127.x.y.z address other than 127.0.0.1", annReverseBind, v)
		}
		rt.Bind = ip.String()
	}

	return rt, nil
}

// parseReverseForward parses "host:port" or "listen-port:host:port". The listen
// port defaults to the destination port, which the job then uses unchanged; a
// destination port below 1024 needs an explicit listen port.
func parseReverseForward(spec string) (ReverseForward, error) {
	parts := strings.Split(spec, ":")
	var f ReverseForward
	var err error
	switch len(parts) {
	case 2:
		f.Host = parts[0]
		if f.Port, err = parseReversePort(parts[1]); err != nil {
			return f, fmt.Errorf("%q: %w", spec, err)
		}
		f.ListenPort = f.Port
	case 3:
		if f.ListenPort, err = parseReversePort(parts[0]); err != nil {
			return f, fmt.Errorf("%q: listen %w", spec, err)
		}
		f.Host = parts[1]
		if f.Port, err = parseReversePort(parts[2]); err != nil {
			return f, fmt.Errorf("%q: %w", spec, err)
		}
	default:
		return f, fmt.Errorf("%q: expected host:port or listen-port:host:port", spec)
	}
	if len(f.Host) > 253 || !reverseHostPattern.MatchString(f.Host) {
		return f, fmt.Errorf("%q: %q is not a hostname or IP address", spec, f.Host)
	}
	if f.ListenPort < reversePrivilegedPortLimit {
		return f, fmt.Errorf("%q: port %d is below %d, which an unprivileged sshd cannot bind on the compute node; use listen-port:host:port with a higher listen port", spec, f.ListenPort, reversePrivilegedPortLimit)
	}
	return f, nil
}

func parseReversePort(s string) (int32, error) {
	port, err := strconv.ParseInt(s, 10, 32)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("port %q is not in 1..65535", s)
	}
	return int32(port), nil
}

// reverseBindAddress picks the loopback address a pod's forwards listen on inside
// the job. Two pods of the same user on one compute node must not share it, so it
// is derived from the pod UID rather than fixed; 127.0.0.0/24 is left alone.
func reverseBindAddress(podUID string) string {
	h := fnv.New32a()
	h.Write([]byte(podUID))
	s := h.Sum32()
	return fmt.Sprintf("127.%d.%d.%d", 1+(s>>16)%254, (s>>8)&0xff, 1+(s&0xff)%254)
}

// reverseHostAliases lists the names a forward's host resolves to inside the job:
// the host as given plus, for a Service FQDN, the shorter forms cluster DNS would
// also answer. The bare Service name only holds in the pod's own namespace.
func reverseHostAliases(host, podNamespace string) []string {
	names := []string{host}
	parts := strings.Split(host, ".")
	if len(parts) >= 4 && parts[2] == "svc" {
		name, ns := parts[0], parts[1]
		names = append(names, name+"."+ns+".svc", name+"."+ns)
		if ns == podNamespace {
			names = append(names, name)
		}
	}
	return names
}

// reverseRelay is the owner-only relay the job runs in front of the tunnel's unix
// sockets.
//
//go:embed templates/reverse-relay.py
var reverseRelay string

// reversePreExec is the shell the job runs before its workload. It starts the
// relay that holds the pod's loopback address for the whole job, maps the
// forwarded names to that address in the container's /etc/hosts, and waits until
// the shadow has bound every forward in the socket directory, which happens only
// once the shadow has learned which node the job landed on. A SOCKS proxy is
// waited for on 127.0.0.1 and must belong to the job's user. It assumes a bash
// batch script and a Singularity or Apptainer runtime, as the mesh pre-exec does.
//
// The hosts file stays in node-local temporary space and never goes into
// $INTERLINK_SOCKET_DIR: only the batch node runs the relay, so ranks on other
// nodes must not resolve the names to an address nobody holds there.
// reverseHostsBind mounts the hosts file for Singularity and Apptainer. Apptainer
// reads SINGULARITY_BIND only while APPTAINER_BIND is unset, so the job's own
// binds from the legacy variable are carried into APPTAINER_BIND.
const reverseHostsBind = `il_rev_ab=${APPTAINER_BIND:-${SINGULARITY_BIND:-}}
export SINGULARITY_BIND="${SINGULARITY_BIND:+$SINGULARITY_BIND,}$il_rev_hosts:/etc/hosts"
export APPTAINER_BIND="${il_rev_ab:+$il_rev_ab,}$il_rev_hosts:/etc/hosts"
`

func reversePreExec(rt *ReverseTunnel, podNamespace string) string {
	seen := map[string]bool{}
	var hosts []string
	for _, f := range rt.Forwards {
		for _, name := range reverseHostAliases(f.Host, podNamespace) {
			if !seen[name] {
				seen[name] = true
				hosts = append(hosts, name)
			}
		}
	}
	timeout := fmt.Sprintf("[ $(( $(date +%%s) - il_rev_t0 )) -ge %d ] && { echo 'interLink: the reverse tunnel did not come up within %ds' >&2; exit 1; }\n",
		reverseTunnelWaitSeconds, reverseTunnelWaitSeconds)

	var b strings.Builder
	b.WriteString("# interLink reverse tunnel: cluster endpoints reachable from this job\n")
	b.WriteString("il_rev_d=${INTERLINK_SOCKET_DIR:-}\n")
	b.WriteString("[ -n \"$il_rev_d\" ] || { echo 'interLink: the reverse tunnel needs a plugin with SocketDirRoot set' >&2; exit 1; }\n")
	b.WriteString("il_rev_t0=$(date +%s)\n")
	if len(rt.Forwards) > 0 {
		fmt.Fprintf(&b, "# Node-local on purpose: only the batch node has a relay behind %s.\n", rt.Bind)
		b.WriteString("il_rev_hosts=$(mktemp \"${TMPDIR:-/tmp}/interlink-hosts.XXXXXX\") || exit 1\n")
		fmt.Fprintf(&b, "{ cat /etc/hosts; echo '%s %s'; } > \"$il_rev_hosts\"\n", rt.Bind, strings.Join(hosts, " "))
		b.WriteString(reverseHostsBind)
		b.WriteString("cat > \"$il_rev_d/rev-relay.py\" <<'EORELAY'\n")
		b.WriteString(reverseRelay)
		if !strings.HasSuffix(reverseRelay, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("EORELAY\n")
		// -I: a module loaded by the batch script must not reach the relay through
		// PYTHONPATH or PYTHONHOME.
		fmt.Fprintf(&b, "%s -I \"$il_rev_d/rev-relay.py\" %s", rt.PythonPath, rt.Bind)
		var ready []string
		for _, f := range rt.Forwards {
			sock := fmt.Sprintf("\"$il_rev_d/rev-%d.sock\"", f.ListenPort)
			fmt.Fprintf(&b, " %d=%s", f.ListenPort, sock)
			ready = append(ready, "[ -S "+sock+" ]")
		}
		b.WriteString(" &\n")
		b.WriteString("il_rev_relay=$!\n")
		fmt.Fprintf(&b, "until [ -e \"$il_rev_d/rev-relay.ready\" ] && %s; do\n", strings.Join(ready, " && "))
		b.WriteString("  kill -0 \"$il_rev_relay\" 2>/dev/null || { echo 'interLink: the reverse relay stopped, see above' >&2; exit 1; }\n")
		b.WriteString("  " + timeout)
		b.WriteString("  sleep 2\n")
		b.WriteString("done\n")
	}
	if rt.SocksPort != 0 {
		// Anyone on the node may bind 127.0.0.1 first: wait for the listener to be
		// the job user's own, and fail if it is somebody else's.
		port := fmt.Sprintf("%04X", rt.SocksPort)
		b.WriteString("il_rev_uid=$(id -u)\n")
		b.WriteString(`il_rev_owner() { awk -v p=":$1" -v u="$il_rev_uid" 'NR>1 && $4=="0A" && ($2=="0100007F"p || $2=="00000000"p) {print ($8==u ? "own" : "other"); exit}' /proc/net/tcp; }` + "\n")
		fmt.Fprintf(&b, "until [ \"$(il_rev_owner %s)\" = own ]; do\n", port)
		fmt.Fprintf(&b, "  [ \"$(il_rev_owner %s)\" = other ] && { echo 'interLink: another user listens on the SOCKS port' >&2; exit 1; }\n", port)
		b.WriteString("  " + timeout)
		b.WriteString("  sleep 2\n")
		b.WriteString("done\n")
	}
	b.WriteString("echo \"interLink: reverse tunnel ready after $(( $(date +%s) - il_rev_t0 ))s\"\n")
	return b.String()
}

// addReversePreExec prepends the tunnel set-up to the pod's pre-exec, the way the
// mesh does, and records it on the Kubernetes object.
func (p *Provider) addReversePreExec(ctx context.Context, pod *v1.Pod, rt *ReverseTunnel) error {
	if pod.Annotations == nil {
		pod.Annotations = make(map[string]string)
	}
	pod.Annotations["slurm-job.vk.io/pre-exec"] = reversePreExec(rt, pod.Namespace) + pod.Annotations["slurm-job.vk.io/pre-exec"]
	log.G(ctx).Infof("Added reverse tunnel pre-exec to pod %s/%s (%d forwards, socks port %d, bind %s)",
		pod.Namespace, pod.Name, len(rt.Forwards), rt.SocksPort, rt.Bind)

	patch, err := json.Marshal(map[string]interface{}{
		"metadata": map[string]interface{}{"annotations": pod.Annotations},
	})
	if err != nil {
		return err
	}
	_, err = p.clientSet.CoreV1().Pods(pod.Namespace).Patch(ctx, pod.Name, k8stypes.StrategicMergePatchType, patch, metav1.PatchOptions{})
	return err
}
