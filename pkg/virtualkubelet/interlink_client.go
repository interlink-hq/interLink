package virtualkubelet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/containerd/containerd/log"
	types "github.com/interlink-hq/interlink/pkg/interlink"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	trace "go.opentelemetry.io/otel/trace"
)

// InterlinkStatusError is returned when the interLink API answers with a status code other than 200.
type InterlinkStatusError struct {
	// Op is the operation that failed (e.g. "create", "delete").
	Op string
	// StatusCode is the HTTP status code returned by the interLink API.
	StatusCode int
	// Body is the response body, which usually carries the reason of the failure.
	Body string
}

func (e *InterlinkStatusError) Error() string {
	msg := fmt.Sprintf("interLink %s failed with status code %d", e.Op, e.StatusCode)
	if body := strings.TrimSpace(e.Body); body != "" {
		msg += ": " + body
	}
	return msg
}

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

// newSessionContext returns an identifier that lets a single call be followed end-to-end, from the VK to the API and
// down to the plugin (e.g. interlink-slurm-plugin).
func newSessionContext(prefix string) string {
	return prefix + "#" + strconv.Itoa(rand.Intn(100000)) // #nosec G404 -- not security sensitive, only used to correlate logs
}

// interlinkCall describes a single REST call to the interLink API.
type interlinkCall struct {
	// op is the human readable operation name used in errors.
	op string
	// spanName is the name of the tracing span wrapping the HTTP call.
	spanName string
	method   string
	path     string
	// body is marshalled to JSON when not nil.
	body    any
	session string
	// attrs are extra tracing attributes, e.g. the pod the call is about.
	attrs      []attribute.KeyValue
	httpClient *http.Client
}

func (c *InterlinkClient) readToken() (string, error) {
	if c.tokenFile == "" {
		return "", nil
	}
	// The file is read on every call so that a rotated token is picked up.
	token, err := os.ReadFile(c.tokenFile)
	if err != nil {
		return "", fmt.Errorf("failed to read VK token file: %w", err)
	}
	return strings.TrimSpace(string(token)), nil
}

// roundTrip sends the call and returns the response, whose body must be closed by the caller.
// The tracing span covers the time until the response headers are received.
func (c *InterlinkClient) roundTrip(ctx context.Context, call interlinkCall) (*http.Response, error) {
	sessionMessage := GetSessionContextMessage(call.session)

	var reader io.Reader
	if call.body != nil {
		bodyBytes, err := json.Marshal(call.body)
		if err != nil {
			return nil, fmt.Errorf("%sfailed to marshal %s request: %w", sessionMessage, call.op, err)
		}
		reader = bytes.NewReader(bodyBytes)
	}

	req, err := http.NewRequestWithContext(ctx, call.method, c.endpoint+call.path, reader)
	if err != nil {
		return nil, fmt.Errorf("%sfailed to build %s request: %w", sessionMessage, call.op, err)
	}

	token, err := c.readToken()
	if err != nil {
		return nil, fmt.Errorf("%s%w", sessionMessage, err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	AddSessionContext(req, call.session)

	startHTTPCall := time.Now().UnixMicro()
	attrs := make([]attribute.KeyValue, 0, len(call.attrs)+1)
	attrs = append(attrs, call.attrs...)
	attrs = append(attrs, attribute.Int64("start.timestamp", startHTTPCall))
	_, span := otel.Tracer("interlink-service").Start(ctx, call.spanName, trace.WithAttributes(attrs...))
	defer span.End()

	resp, err := call.httpClient.Do(req) // #nosec G704 -- the endpoint comes from the operator configuration and is validated in NewInterlinkClient
	if err != nil {
		types.SetDurationSpan(startHTTPCall, span)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		// The error is not formatted with the request: it would contain the Authorization header.
		return nil, fmt.Errorf("%s%s request failed: %w", sessionMessage, call.op, err)
	}

	types.SetDurationSpan(startHTTPCall, span, types.WithHTTPReturnCode(resp.StatusCode))
	return resp, nil
}

// doJSON performs the call and returns the whole response body.
// A status code other than 200 is reported as an *InterlinkStatusError.
func (c *InterlinkClient) doJSON(ctx context.Context, call interlinkCall) ([]byte, error) {
	call.httpClient = c.httpClient

	resp, err := c.roundTrip(ctx, call)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	sessionMessage := GetSessionContextMessage(call.session)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%sfailed to read %s response: %w", sessionMessage, call.op, err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s%w", sessionMessage, &InterlinkStatusError{Op: call.op, StatusCode: resp.StatusCode, Body: string(body)})
	}

	return body, nil
}

// Ping checks that the interLink API is reachable. A non-200 answer is not an error: it is reported through the returned
// status code, together with the response body. Errors are only returned when no answer could be obtained.
func (c *InterlinkClient) Ping(ctx context.Context) (int, string, error) {
	body, err := c.doJSON(ctx, interlinkCall{
		op:       "ping",
		spanName: "PingHttpCall",
		method:   http.MethodPost,
		path:     "/pinglink",
		session:  newSessionContext("PingInterLink"),
	})

	var statusErr *InterlinkStatusError
	switch {
	case errors.As(err, &statusErr):
		return statusErr.StatusCode, statusErr.Body, nil
	case err != nil:
		return 0, "", err
	}

	return http.StatusOK, string(body), nil
}
