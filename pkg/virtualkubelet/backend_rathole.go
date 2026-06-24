package virtualkubelet

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/containerd/containerd/log"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
)

type RatholeBackend struct {
	cfg           Network
	dynamicClient dynamic.Interface
	provider      *Provider
}

func (b *RatholeBackend) bindProvider(p *Provider) {
	b.provider = p
	if b.dynamicClient == nil {
		b.dynamicClient = p.dynamicClient
	}
}

func (b *RatholeBackend) Name() string {
	return tunnelTypeRathole
}

func (b *RatholeBackend) ServerResources(ctx context.Context, td WstunnelTemplateData) error {
	if b.cfg.RatholeCAIssuerName == "" {
		return nil
	}
	if b.provider == nil {
		return fmt.Errorf("rathole backend not bound to provider")
	}
	return b.provider.applyRatholeTLSResources(ctx, td)
}

func (b *RatholeBackend) ClientCommand(ctx context.Context, td WstunnelTemplateData, pod *v1.Pod) (string, error) {
	if b.provider == nil || b.provider.clientSet == nil {
		return "", fmt.Errorf("rathole backend requires initialized Kubernetes client")
	}

	ratholeEndpoint := fmt.Sprintf("rathole-%s.%s", td.Name, td.WildcardDNS)
	ratholeEndpoint = sanitizeFullDNSName(ratholeEndpoint)
	if td.WildcardDNS == "" {
		ratholeEndpoint = td.Name
	}

	ratholeURL := b.cfg.RatholeExecutableURL
	if ratholeURL == "" {
		ratholeURL = DefaultRatholeExecutableURL
	}

	if b.cfg.RatholeCAIssuerName != "" {
		clientCertSecretName := td.Name + "-rathole-client-tls"
		if err := b.provider.waitForRatholeCertSecret(ctx, clientCertSecretName, td.Namespace); err != nil {
			return "", fmt.Errorf("rathole client certificate not ready: %w", err)
		}

		certSecret, err := b.provider.clientSet.CoreV1().Secrets(td.Namespace).Get(ctx, clientCertSecretName, metav1.GetOptions{})
		if err != nil {
			return "", fmt.Errorf("failed to read rathole client certificate secret: %w", err)
		}

		for _, key := range []string{"ca.crt", "tls.crt", "tls.key"} {
			if len(certSecret.Data[key]) == 0 {
				return "", fmt.Errorf("rathole client certificate secret %s/%s is missing required key %q", td.Namespace, clientCertSecretName, key)
			}
		}

		caCrtB64 := base64.StdEncoding.EncodeToString(certSecret.Data["ca.crt"])
		clientCrtB64 := base64.StdEncoding.EncodeToString(certSecret.Data["tls.crt"])
		clientKeyB64 := base64.StdEncoding.EncodeToString(certSecret.Data["tls.key"])

		var tomlBuilder strings.Builder
		fmt.Fprintf(&tomlBuilder, "[client]\nremote_addr = \"%s:443\"\n\n", ratholeEndpoint)
		tomlBuilder.WriteString("[client.transport]\ntype = \"tls\"\n\n")
		tomlBuilder.WriteString("[client.transport.tls]\n")
		fmt.Fprintf(&tomlBuilder, "hostname = \"%s\"\n", ratholeEndpoint)
		tomlBuilder.WriteString("trusted_root = \"/tmp/rathole-ca.crt\"\n")
		tomlBuilder.WriteString("cert = \"/tmp/rathole-client.crt\"\n")
		tomlBuilder.WriteString("key = \"/tmp/rathole-client.key\"\n\n")
		for _, port := range td.ExposedPorts {
			if strings.ToUpper(port.Protocol) == protocolUDP {
				log.G(ctx).Debugf("Skipping UDP port %d in rathole client config (TLS transport forwards TCP only)", port.Port)
				continue
			}
			fmt.Fprintf(&tomlBuilder, "[client.services.p%d]\ntoken = \"%s\"\nlocal_addr = \"127.0.0.1:%d\"\n\n",
				port.Port, td.RandomPassword, port.Port)
		}

		configB64 := base64.StdEncoding.EncodeToString([]byte(tomlBuilder.String()))
		ratholeCmd := b.cfg.RatholeCommand
		if ratholeCmd == "" {
			ratholeCmd = DefaultRatholeCommand
		}
		if strings.Count(ratholeCmd, "%s") != 5 {
			return "", fmt.Errorf("RatholeCommand must have exactly 5 %%s format verbs (url, ca, cert, key, toml); got %d in %q",
				strings.Count(ratholeCmd, "%s"), b.cfg.RatholeCommand)
		}
		return fmt.Sprintf(ratholeCmd, ratholeURL, caCrtB64, clientCrtB64, clientKeyB64, configB64), nil
	}

	if pod != nil {
		log.G(ctx).Debugf("RatholeCAIssuerName not set; using WebSocket transport for pod %s/%s", pod.Namespace, pod.Name)
	}
	var tomlBuilder strings.Builder
	fmt.Fprintf(&tomlBuilder, "[client]\nremote_addr = \"%s:80\"\n\n", ratholeEndpoint)
	tomlBuilder.WriteString("[client.transport]\ntype = \"websocket\"\n\n")
	for _, port := range td.ExposedPorts {
		if strings.ToUpper(port.Protocol) == protocolUDP {
			log.G(ctx).Debugf("Skipping UDP port %d in rathole client config (websocket transport forwards TCP only)", port.Port)
			continue
		}
		fmt.Fprintf(&tomlBuilder, "[client.services.p%d]\ntoken = \"%s\"\nlocal_addr = \"127.0.0.1:%d\"\n\n",
			port.Port, td.RandomPassword, port.Port)
	}

	configB64 := base64.StdEncoding.EncodeToString([]byte(tomlBuilder.String()))
	ratholeWSCmd := b.cfg.RatholeWSCommand
	if ratholeWSCmd == "" {
		ratholeWSCmd = DefaultRatholeWSCommand
	}
	if strings.Count(ratholeWSCmd, "%s") != 2 {
		return "", fmt.Errorf("RatholeWSCommand must have exactly 2 %%s format verbs (url, toml); got %d in %q",
			strings.Count(ratholeWSCmd, "%s"), b.cfg.RatholeWSCommand)
	}

	return fmt.Sprintf(ratholeWSCmd, ratholeURL, configB64), nil
}

func (b *RatholeBackend) ClientAnnotationKey() string {
	return annRatholeClientCmds
}

func (b *RatholeBackend) KubernetesTemplate() (string, error) {
	content, err := defaultRatholeTemplate.ReadFile("templates/rathole-template.yaml")
	if err != nil {
		return "", fmt.Errorf("failed to read embedded rathole template: %w", err)
	}
	return string(content), nil
}

func (b *RatholeBackend) CleanupResources(ctx context.Context, name, namespace string) error {
	if b.provider == nil || b.provider.dynamicClient == nil {
		return nil
	}

	for _, certName := range []string{name + "-rathole-server-tls", name + "-rathole-client-tls"} {
		if err := b.provider.deleteUnstructuredResource(ctx, certManagerCertGVR, certName, namespace); err != nil {
			return fmt.Errorf("failed to delete rathole cert-manager Certificate %s/%s: %w", namespace, certName, err)
		}
		log.G(ctx).Infof("Deleted rathole cert-manager Certificate %s/%s", namespace, certName)
	}

	if err := b.provider.deleteUnstructuredResource(ctx, traefikIngressRouteTCPGVR, name, namespace); err != nil {
		return fmt.Errorf("failed to delete rathole IngressRouteTCP %s/%s: %w", namespace, name, err)
	}
	log.G(ctx).Infof("Deleted rathole IngressRouteTCP %s/%s", namespace, name)

	return nil
}
