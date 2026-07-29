package virtualkubelet

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/containerd/containerd/log"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// DefaultSSHPort is the login node port used when none is configured.
	DefaultSSHPort = 22
	// DefaultSSHKeySecretKey is the key holding the private key inside KeySecret.
	DefaultSSHKeySecretKey = "id_ed25519"
	// DefaultSSHKeytabSecretKey is the key holding the keytab inside KeytabSecret.
	DefaultSSHKeytabSecretKey = "user.keytab"
	// DefaultSSHNodeWaitTimeout bounds the wait for the plugin to report a compute
	// node. Batch queues routinely make a pod wait hours before it starts.
	DefaultSSHNodeWaitTimeout = "2h"
)

// defaultSSHNodeWait is DefaultSSHNodeWaitTimeout as a duration, so falling back to
// it needs no parsing. TestDefaultSSHNodeWaitMatchesTimeout keeps the two in step.
const defaultSSHNodeWait = 2 * time.Hour

// defaultSSHTunnelImage returns the image the SSH shadow runs, pinned to this
// virtual kubelet's own version so the two are released together.
func defaultSSHTunnelImage() string {
	return "ghcr.io/interlink-hq/interlink/ssh-tunnel:" + KubeletVersion
}

// isSSHShadow reports whether shadows are rendered as SSH port-forwards.
func (p *Provider) isSSHShadow() bool {
	return p.config.Network.ShadowMode == ShadowModeSSH
}

// NormalizeShadowConfig fills in shadow defaults and rejects combinations that
// cannot work, so a misconfigured deployment fails at startup rather than when the
// first pod with an exposed port shows up.
func NormalizeShadowConfig(config *Config) error {
	n := &config.Network

	n.ShadowMode = strings.ToLower(strings.TrimSpace(n.ShadowMode))
	switch n.ShadowMode {
	case "":
		n.ShadowMode = ShadowModeWstunnel
	case ShadowModeWstunnel, ShadowModeSSH:
	default:
		return fmt.Errorf("unknown Network.ShadowMode %q: expected %q or %q", n.ShadowMode, ShadowModeWstunnel, ShadowModeSSH)
	}

	if n.ShadowMode != ShadowModeSSH {
		return nil
	}

	// Mesh needs the compute node to dial out to the cluster, which is exactly what
	// an SSH-only site cannot do. Combining the two is tracked separately.
	if n.FullMesh {
		return fmt.Errorf("Network.FullMesh is not supported with ShadowMode %q: the SSH shadow only exposes the offloaded pod's ports to the cluster, it does not give the pod access back into it (see interlink-hq/interLink#548)", ShadowModeSSH)
	}

	s := &n.SSH
	if strings.TrimSpace(s.LoginHost) == "" {
		return fmt.Errorf("Network.SSH.LoginHost is required with ShadowMode %q", ShadowModeSSH)
	}
	if strings.TrimSpace(s.User) == "" {
		return fmt.Errorf("Network.SSH.User is required with ShadowMode %q", ShadowModeSSH)
	}

	if s.Port == 0 {
		s.Port = DefaultSSHPort
	}
	if s.Image == "" {
		s.Image = defaultSSHTunnelImage()
	}
	if s.NodeWaitTimeout == "" {
		s.NodeWaitTimeout = DefaultSSHNodeWaitTimeout
	}
	if _, err := time.ParseDuration(s.NodeWaitTimeout); err != nil {
		return fmt.Errorf("invalid Network.SSH.NodeWaitTimeout %q: %w", s.NodeWaitTimeout, err)
	}
	if s.ReplicateCredentials == nil {
		replicate := true
		s.ReplicateCredentials = &replicate
	}

	s.Auth = strings.ToLower(strings.TrimSpace(s.Auth))
	switch s.Auth {
	case "":
		s.Auth = SSHAuthPublicKey
		fallthrough
	case SSHAuthPublicKey:
		if strings.TrimSpace(s.KeySecret) == "" {
			return fmt.Errorf("Network.SSH.KeySecret is required with Auth %q", SSHAuthPublicKey)
		}
		if s.KeySecretKey == "" {
			s.KeySecretKey = DefaultSSHKeySecretKey
		}
	case SSHAuthKerberos:
		if strings.TrimSpace(s.KeytabSecret) == "" {
			return fmt.Errorf("Network.SSH.KeytabSecret is required with Auth %q", SSHAuthKerberos)
		}
		if strings.TrimSpace(s.Principal) == "" {
			return fmt.Errorf("Network.SSH.Principal is required with Auth %q", SSHAuthKerberos)
		}
		if s.KeytabSecretKey == "" {
			s.KeytabSecretKey = DefaultSSHKeytabSecretKey
		}
	default:
		return fmt.Errorf("unknown Network.SSH.Auth %q: expected %q or %q", s.Auth, SSHAuthPublicKey, SSHAuthKerberos)
	}

	return nil
}

// sshNodeWaitSeconds converts the configured wait into whole seconds for the shell
// loop in the template. NormalizeShadowConfig has already validated the duration,
// so the fallback only guards against a Provider built without it.
func (p *Provider) sshNodeWaitSeconds() int {
	d, err := time.ParseDuration(p.config.Network.SSH.NodeWaitTimeout)
	if err != nil {
		d = defaultSSHNodeWait
	}
	return int(d.Seconds())
}

// sshCredentialSecret returns the Secret name the configured auth method needs.
func sshCredentialSecret(s SSHTunnel) string {
	if s.Auth == SSHAuthKerberos {
		return s.KeytabSecret
	}
	return s.KeySecret
}

// replicateShadowCredentials copies the SSH credential Secret and any supporting
// ConfigMaps from the virtual kubelet's own namespace into the shadow's namespace.
//
// Shadows follow the offloaded pod, which for multi-tenant setups means arbitrary
// per-user namespaces; without this every one of them would have to be seeded with
// the credential by hand before offloading could work.
func (p *Provider) replicateShadowCredentials(ctx context.Context, identity shadowResourceIdentity) error {
	s := p.config.Network.SSH
	if s.ReplicateCredentials == nil || !*s.ReplicateCredentials {
		return nil
	}

	source := p.config.Namespace
	if source == "" || source == identity.Namespace {
		return nil
	}

	if name := sshCredentialSecret(s); name != "" {
		if err := p.replicateSecret(ctx, source, identity.Namespace, name); err != nil {
			return err
		}
	}
	for _, name := range []string{s.Krb5ConfigMap, s.KnownHostsConfigMap} {
		if name == "" {
			continue
		}
		if err := p.replicateConfigMap(ctx, source, identity.Namespace, name); err != nil {
			return err
		}
	}
	return nil
}

func (p *Provider) replicateSecret(ctx context.Context, source, target, name string) error {
	src, err := p.clientSet.CoreV1().Secrets(source).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to read SSH credential secret %s/%s: %w", source, name, err)
	}
	copied := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: target},
		Type:       src.Type,
		Data:       src.Data,
	}
	if err := p.applyOrUpdateSecret(ctx, copied); err != nil {
		return fmt.Errorf("failed to replicate secret %s into %s: %w", name, target, err)
	}
	log.G(ctx).Infof("Replicated SSH credential secret %s from %s to %s", name, source, target)
	return nil
}

func (p *Provider) replicateConfigMap(ctx context.Context, source, target, name string) error {
	src, err := p.clientSet.CoreV1().ConfigMaps(source).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to read configmap %s/%s: %w", source, name, err)
	}
	copied := &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: target},
		Data:       src.Data,
		BinaryData: src.BinaryData,
	}
	if err := p.applyOrUpdateConfigMap(ctx, copied); err != nil {
		return fmt.Errorf("failed to replicate configmap %s into %s: %w", name, target, err)
	}
	log.G(ctx).Infof("Replicated configmap %s from %s to %s", name, source, target)
	return nil
}

// warnOnUDPPorts reports ports the SSH shadow cannot carry. ssh -L forwards TCP
// only, so a UDP port is silently unreachable; say so rather than let it look like
// a broken tunnel.
func warnOnUDPPorts(ctx context.Context, ports []PortMapping) {
	var udp []string
	for _, port := range ports {
		if strings.EqualFold(port.Protocol, "UDP") {
			udp = append(udp, fmt.Sprintf("%d", port.Port))
		}
	}
	if len(udp) > 0 {
		log.G(ctx).Warningf("SSH shadow cannot forward UDP ports %s: ssh -L is TCP only", strings.Join(udp, ", "))
	}
}
