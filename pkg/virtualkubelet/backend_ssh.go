package virtualkubelet

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type SSHBackend struct {
	cfg      Network
	provider *Provider
}

func (b *SSHBackend) bindProvider(p *Provider) {
	b.provider = p
}

func (b *SSHBackend) Name() string {
	return tunnelTypeSSH
}

func (b *SSHBackend) ServerResources(_ context.Context, _ WstunnelTemplateData) error {
	return nil
}

func (b *SSHBackend) ClientCommand(ctx context.Context, td WstunnelTemplateData, pod *v1.Pod) (string, error) {
	if b.provider == nil || b.provider.clientSet == nil {
		return "", fmt.Errorf("ssh backend requires initialized Kubernetes client")
	}
	if strings.TrimSpace(b.cfg.SSHJumpHost) == "" {
		return "", fmt.Errorf("SSHJumpHost is required when TunnelType is %q", tunnelTypeSSH)
	}
	if strings.TrimSpace(b.cfg.SSHJumpKeySecretName) == "" {
		return "", fmt.Errorf("SSHJumpKeySecretName is required when TunnelType is %q", tunnelTypeSSH)
	}

	secretNamespace := strings.TrimSpace(b.cfg.SSHJumpKeySecretNamespace)
	if secretNamespace == "" && b.provider != nil {
		secretNamespace = strings.TrimSpace(b.provider.config.Namespace)
	}
	if secretNamespace == "" && pod != nil {
		secretNamespace = pod.Namespace
	}
	if secretNamespace == "" {
		return "", fmt.Errorf("cannot resolve SSHJumpKeySecretNamespace")
	}

	secret, err := b.provider.clientSet.CoreV1().Secrets(secretNamespace).Get(ctx, b.cfg.SSHJumpKeySecretName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to read SSH jump key secret %s/%s: %w", secretNamespace, b.cfg.SSHJumpKeySecretName, err)
	}

	privateKey := secret.Data["id_rsa"]
	if len(privateKey) == 0 {
		privateKey = secret.Data["id_ed25519"]
	}
	if len(privateKey) == 0 {
		return "", fmt.Errorf("secret %s/%s must contain id_rsa or id_ed25519", secretNamespace, b.cfg.SSHJumpKeySecretName)
	}
	keyB64 := base64.StdEncoding.EncodeToString(privateKey)

	remoteHost := strings.TrimSpace(b.cfg.SSHRemoteHost)
	if remoteHost == "" {
		remoteHost = "localhost"
	}

	forwardSpecs := make([]string, 0, len(td.ExposedPorts))
	for _, port := range td.ExposedPorts {
		if strings.ToUpper(port.Protocol) == protocolUDP {
			continue
		}
		forwardSpecs = append(forwardSpecs, fmt.Sprintf("0.0.0.0:%d:%s:%d", port.Port, remoteHost, port.Port))
	}
	if len(forwardSpecs) == 0 {
		return "", fmt.Errorf("no TCP exposed ports available for ssh tunnel")
	}

	if strings.TrimSpace(b.cfg.SSHCommand) != "" {
		// SSHCommand format: first %s is base64 private key, second %s is jump host,
		// third %s is remote host, remaining %s are one per forward spec.
		expected := len(forwardSpecs) + 3
		if strings.Count(b.cfg.SSHCommand, "%s") != expected {
			return "", fmt.Errorf("SSHCommand must have exactly %d %%s format verbs (key, jump-host, remote-host, and one per forward spec); got %d",
				expected, strings.Count(b.cfg.SSHCommand, "%s"))
		}
		args := make([]any, 0, expected)
		args = append(args, keyB64, b.cfg.SSHJumpHost, remoteHost)
		for _, spec := range forwardSpecs {
			args = append(args, spec)
		}
		return fmt.Sprintf(b.cfg.SSHCommand, args...), nil
	}

	var forwardArgs strings.Builder
	for _, spec := range forwardSpecs {
		fmt.Fprintf(&forwardArgs, " -L %s", spec)
	}

	cmd := fmt.Sprintf(
		"echo %s | base64 -d > /tmp/ssh_jump_key && chmod 600 /tmp/ssh_jump_key && ssh -i /tmp/ssh_jump_key -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=/tmp/ssh_known_hosts -N -J %s%s %s &",
		keyB64,
		b.cfg.SSHJumpHost,
		forwardArgs.String(),
		remoteHost,
	)
	return cmd, nil
}

func (b *SSHBackend) ClientAnnotationKey() string {
	return annSSHClientCmds
}

func (b *SSHBackend) KubernetesTemplate() (string, error) {
	// SSH backend uses the default (wstunnel) shadow resources template.
	return "", nil
}

func (b *SSHBackend) CleanupResources(_ context.Context, _, _ string) error {
	return nil
}
