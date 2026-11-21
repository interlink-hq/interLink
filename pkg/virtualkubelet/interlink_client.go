package virtualkubelet

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/containerd/containerd/log"
)

// InterlinkClient performs the REST calls from the Virtual Kubelet to the interLink API.
// It is safe for concurrent use.
type InterlinkClient struct {
	endpoint            string
	tokenFile           string
	jobScriptBuilderURL string
	httpClient          *http.Client
	logHTTPClient       *http.Client
}

// NewInterlinkClient builds a client for the interLink API described by config.
//
// logTransport, when not nil, is cloned and used for log retrieval, with keep-alives disabled because the response body
// is streamed to the kubelet and must not be pooled. When nil, log retrieval uses the same client as the other calls.
func NewInterlinkClient(ctx context.Context, config Config, logTransport *http.Transport) (*InterlinkClient, error) {
	endpoint, err := resolveInterlinkEndpoint(config.InterlinkURL, config.InterlinkPort)
	if err != nil {
		return nil, err
	}

	httpClient, err := createTLSHTTPClient(ctx, config.TLS)
	if err != nil {
		return nil, fmt.Errorf("failed to create TLS HTTP client: %w", err)
	}

	logHTTPClient := httpClient
	if logTransport != nil {
		transport := logTransport.Clone()
		transport.DisableKeepAlives = true
		transport.MaxIdleConnsPerHost = -1
		logHTTPClient = &http.Client{Transport: transport}
	}

	log.G(ctx).Info("InterLink endpoint: ", endpoint)

	return &InterlinkClient{
		endpoint:            endpoint,
		tokenFile:           config.VKTokenFile,
		jobScriptBuilderURL: config.JobScriptBuilderURL,
		httpClient:          httpClient,
		logHTTPClient:       logHTTPClient,
	}, nil
}

// resolveInterlinkEndpoint returns the base URL used to reach the interLink API.
// Unix sockets are reached through a placeholder host: the transport dials the socket regardless of the host.
func resolveInterlinkEndpoint(interLinkURL string, interLinkPort string) (string, error) {
	switch {
	case strings.HasPrefix(interLinkURL, "unix://"):
		return "http://unix", nil
	case strings.HasPrefix(interLinkURL, "http://"), strings.HasPrefix(interLinkURL, "https://"):
		if interLinkPort == "" {
			return interLinkURL, nil
		}
		return interLinkURL + ":" + interLinkPort, nil
	default:
		return "", fmt.Errorf("InterlinkURL %q should either start with unix:// or http(s)://", interLinkURL)
	}
}
