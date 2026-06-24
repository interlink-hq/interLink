package virtualkubelet

import (
	"context"
	"fmt"
	"strings"

	"github.com/containerd/containerd/log"
	v1 "k8s.io/api/core/v1"
)

type WstunnelBackend struct {
	cfg Network
}

func (b *WstunnelBackend) Name() string {
	return "wstunnel"
}

func (b *WstunnelBackend) ServerResources(_ context.Context, _ WstunnelTemplateData) error {
	return nil
}

func (b *WstunnelBackend) ClientCommand(ctx context.Context, td WstunnelTemplateData, _ *v1.Pod) (string, error) {
	ingressEndpoint := fmt.Sprintf("%s-%s.%s", td.Name, td.Namespace, td.WildcardDNS)
	ingressEndpoint = sanitizeFullDNSName(ingressEndpoint)
	if td.WildcardDNS == "" {
		ingressEndpoint = td.Name
	}

	var rOptions []string
	for _, port := range td.ExposedPorts {
		if strings.ToUpper(port.Protocol) == protocolUDP {
			continue
		}
		rOptions = append(rOptions, fmt.Sprintf("-R tcp://0.0.0.0:%d:localhost:%d", port.Port, port.Port))
	}

	wstunnelCommandTemplate := b.cfg.WstunnelCommand
	if wstunnelCommandTemplate == "" {
		wstunnelCommandTemplate = DefaultWstunnelCommand
	}

	log.G(ctx).Infof("Default ws tunnel command is: %s", wstunnelCommandTemplate)

	return fmt.Sprintf(
		wstunnelCommandTemplate,
		td.RandomPassword,
		strings.Join(rOptions, " "),
		ingressEndpoint,
	), nil
}

func (b *WstunnelBackend) ClientAnnotationKey() string {
	return annWSTunnelClientCmds
}

func (b *WstunnelBackend) KubernetesTemplate() (string, error) {
	return "", nil
}

func (b *WstunnelBackend) CleanupResources(_ context.Context, _, _ string) error {
	return nil
}
