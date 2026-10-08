package virtualkubelet

import (
	"context"
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
	// annReverseBind overrides the 127.x.y.z address the forwards listen on inside
	// the job, normally derived from the pod UID.
	annReverseBind = "interlink.eu/reverse-bind"
	// annReverseNodeExec overrides Network.SSH.Reverse.NodeExec for one pod.
	annReverseNodeExec = "interlink.eu/reverse-node-exec"
	// annReverseOwnerOnly ("true", the default, or "false") controls whether the
	// forwards are reachable by the job's user only or by everyone on the node.
	annReverseOwnerOnly = "interlink.eu/reverse-owner-only"
)

// How the shadow reaches the compute node from the login node to start its sshd.
const (
	// SSHReverseNodeExecSSH runs `ssh <node>` on the login node. Needs the site to
	// allow ssh into a node where the user has a running job.
	SSHReverseNodeExecSSH = "ssh"
	// SSHReverseNodeExecSrun runs `srun --jobid=<job> --overlap` on the login node,
	// inside the job's own allocation. Needs nothing beyond Slurm.
	SSHReverseNodeExecSrun = "srun"
)

const (
	// DefaultSSHReverseSshdPath is where sshd is looked for on the compute node.
	DefaultSSHReverseSshdPath = "/usr/sbin/sshd"
	// DefaultSSHReversePythonPath runs the owner-only relay on the compute node.
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
	// Bind is the 127.x.y.z address the forwards listen on, inside the job.
	Bind string
	// NodeExec is SSHReverseNodeExecSSH or SSHReverseNodeExecSrun.
	NodeExec   string
	OwnerOnly  bool
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
	}
	return nil
}

// parseReverseTunnel turns the pod's reverse annotations into template data,
// rejecting anything that could not work or that the template could not pass to
// ssh safely.
func parseReverseTunnel(pod *v1.Pod, cfg SSHReverse) (*ReverseTunnel, error) {
	ann := pod.Annotations
	rt := &ReverseTunnel{
		NodeExec:   cfg.NodeExec,
		OwnerOnly:  true,
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

	if v := strings.ToLower(strings.TrimSpace(ann[annReverseNodeExec])); v != "" {
		if v != SSHReverseNodeExecSSH && v != SSHReverseNodeExecSrun {
			return nil, fmt.Errorf("%s: %q is not %q or %q", annReverseNodeExec, v, SSHReverseNodeExecSSH, SSHReverseNodeExecSrun)
		}
		rt.NodeExec = v
	}

	if v := strings.TrimSpace(ann[annReverseOwnerOnly]); v != "" {
		ownerOnly, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("%s: %q is not true or false", annReverseOwnerOnly, v)
		}
		rt.OwnerOnly = ownerOnly
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

// reversePreExec is the shell the job runs before its workload: it maps the
// forwarded names to the tunnel's loopback address in the container's /etc/hosts
// and waits for the tunnel, which is up only once the shadow has learned which
// node the job landed on. It assumes a bash batch script and a Singularity or
// Apptainer runtime, as the mesh pre-exec does.
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
	probe := rt.SocksPort
	if len(rt.Forwards) > 0 {
		probe = rt.Forwards[0].ListenPort
	}

	var b strings.Builder
	b.WriteString("# interLink reverse tunnel: cluster endpoints reachable from this job\n")
	if len(hosts) > 0 {
		b.WriteString("il_rev_hosts=${TMPDIR:-/tmp}/interlink-reverse-hosts.$$\n")
		fmt.Fprintf(&b, "{ cat /etc/hosts; echo '%s %s'; } > \"$il_rev_hosts\"\n", rt.Bind, strings.Join(hosts, " "))
		b.WriteString("export SINGULARITY_BIND=\"${SINGULARITY_BIND:+$SINGULARITY_BIND,}$il_rev_hosts:/etc/hosts\"\n")
		b.WriteString("export APPTAINER_BIND=\"${APPTAINER_BIND:+$APPTAINER_BIND,}$il_rev_hosts:/etc/hosts\"\n")
	}
	b.WriteString("il_rev_t0=$(date +%s)\n")
	fmt.Fprintf(&b, "until (exec 3<>/dev/tcp/%s/%d) 2>/dev/null; do\n", rt.Bind, probe)
	fmt.Fprintf(&b, "  if [ $(( $(date +%%s) - il_rev_t0 )) -ge %d ]; then echo 'interLink: the reverse tunnel did not come up within %ds' >&2; exit 1; fi\n", reverseTunnelWaitSeconds, reverseTunnelWaitSeconds)
	b.WriteString("  sleep 2\n")
	b.WriteString("done\n")
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
