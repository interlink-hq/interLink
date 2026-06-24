package virtualkubelet

import (
	"context"
	"fmt"
	"strings"

	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/dynamic"
)

// TunnelBackend encapsulates tunnel-specific server resources, client command generation,
// template selection, and backend cleanup.
type TunnelBackend interface {
	Name() string
	ServerResources(ctx context.Context, td WstunnelTemplateData) error
	ClientCommand(ctx context.Context, td WstunnelTemplateData, pod *v1.Pod) (string, error)
	ClientAnnotationKey() string
	KubernetesTemplate() (string, error)
	CleanupResources(ctx context.Context, name, namespace string) error
}

func newTunnelBackend(cfg Network, dynamicClient dynamic.Interface) (TunnelBackend, error) {
	switch strings.TrimSpace(strings.ToLower(cfg.TunnelType)) {
	case "", "wstunnel":
		return &WstunnelBackend{cfg: cfg}, nil
	case tunnelTypeRathole:
		return &RatholeBackend{cfg: cfg, dynamicClient: dynamicClient}, nil
	case tunnelTypeSSH:
		return &SSHBackend{cfg: cfg}, nil
	default:
		return nil, fmt.Errorf("unsupported tunnel backend %q (supported: \"\", \"wstunnel\", \"rathole\", \"ssh\")", cfg.TunnelType)
	}
}
