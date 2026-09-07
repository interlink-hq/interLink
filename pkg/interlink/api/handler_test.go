package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	types "github.com/interlink-hq/interlink/pkg/interlink"
)

// unixSocketRoundTripper rewrites http+unix URLs to http://unix so the underlying
// transport can dial the configured unix socket.
type unixSocketRoundTripper struct {
	transport http.RoundTripper
}

func tracingSpanRecorder(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(trace.WithSyncer(exporter))
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { require.NoError(t, tp.Shutdown(context.Background())) })
	return exporter
}

func findTracingSpan(t *testing.T, exporter *tracetest.InMemoryExporter, name string) tracetest.SpanStub {
	t.Helper()
	for _, span := range exporter.GetSpans() {
		if span.Name == name {
			return span
		}
	}
	t.Fatalf("no span named %q was exported", name)
	return tracetest.SpanStub{}
}

func tracingAttrValue(attrs []attribute.KeyValue, key string) (attribute.Value, bool) {
	for _, attr := range attrs {
		if string(attr.Key) == key {
			return attr.Value, true
		}
	}
	return attribute.Value{}, false
}

func tracingTestHandler(t *testing.T, status int) *InterLinkHandler {
	t.Helper()
	server, _, client := newUnixTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, `[]`)
	}))
	t.Cleanup(server.Close)
	return &InterLinkHandler{Ctx: context.Background(), SidecarEndpoint: "http+unix://", ClientHTTP: client}
}

func tracingJSON(t *testing.T, value any) *bytes.Reader {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return bytes.NewReader(data)
}

func TestAPITracingCreatesServerAndClientSpans(t *testing.T) {
	exporter := tracingSpanRecorder(t)
	previousPropagator := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { otel.SetTextMapPropagator(previousPropagator) })

	var receivedSession string
	var receivedTraceparent string
	server, _, client := newUnixTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedSession = r.Header.Get("InterLink-Http-Session")
		receivedTraceparent = r.Header.Get("traceparent")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `[]`)
	}))
	defer server.Close()

	h := &InterLinkHandler{Ctx: context.Background(), SidecarEndpoint: "http+unix://", ClientHTTP: client}
	remoteParent := oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID:    oteltrace.TraceID{1, 2, 3},
		SpanID:     oteltrace.SpanID{4, 5, 6},
		TraceFlags: oteltrace.FlagsSampled,
		Remote:     true,
	})
	req := httptest.NewRequest(http.MethodGet, "/pinglink", nil)
	req.Header.Set("InterLink-Http-Session", "Request-test-session")
	req.Header.Set("traceparent", fmt.Sprintf("00-%s-%s-01", remoteParent.TraceID(), remoteParent.SpanID()))

	h.Ping(httptest.NewRecorder(), req)

	serverSpan := findTracingSpan(t, exporter, "PingAPI")
	clientSpan := findTracingSpan(t, exporter, "HTTP GET")
	assert.Equal(t, oteltrace.SpanKindServer, serverSpan.SpanKind)
	assert.Equal(t, remoteParent.SpanID(), serverSpan.Parent.SpanID())
	assert.Equal(t, oteltrace.SpanKindClient, clientSpan.SpanKind)
	assert.Equal(t, serverSpan.SpanContext.SpanID(), clientSpan.Parent.SpanID())
	assert.Equal(t, "Request-test-session", receivedSession)
	assert.NotEmpty(t, receivedTraceparent)

	method, ok := tracingAttrValue(serverSpan.Attributes, "http.request.method")
	require.True(t, ok)
	assert.Equal(t, http.MethodGet, method.AsString())
	status, ok := tracingAttrValue(serverSpan.Attributes, "http.response.status_code")
	require.True(t, ok)
	assert.Equal(t, int64(http.StatusOK), status.AsInt64())
}

func TestDetailedTracingAddsMetadataWithoutValues(t *testing.T) {
	exporter := tracingSpanRecorder(t)
	h := tracingTestHandler(t, http.StatusOK)
	h.Config.Tracing.Detailed = true

	const sensitiveValue = "must-not-appear-in-tracing"
	podRequest := types.PodCreateRequests{
		Pod: v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "trace-pod",
				Namespace:   "trace-ns",
				UID:         "trace-uid",
				Labels:      map[string]string{"team": sensitiveValue},
				Annotations: map[string]string{"example.org/token": sensitiveValue},
			},
			Spec: v1.PodSpec{Containers: []v1.Container{{
				Name:  "worker",
				Image: "registry.example/worker:v1",
				Env:   []v1.EnvVar{{Name: "ACCESS_TOKEN", Value: sensitiveValue}},
			}}},
		},
		Secrets: []v1.Secret{{
			ObjectMeta: metav1.ObjectMeta{Name: "runtime-secret"},
			Data:       map[string][]byte{"token": []byte(sensitiveValue)},
		}},
	}

	w := httptest.NewRecorder()
	h.CreateHandler(w, httptest.NewRequest(http.MethodPost, "/create", tracingJSON(t, podRequest)))
	require.Equal(t, http.StatusOK, w.Code)

	span := findTracingSpan(t, exporter, "CreateAPI")
	detailed, ok := tracingAttrValue(span.Attributes, "interlink.tracing.detailed")
	require.True(t, ok)
	assert.True(t, detailed.AsBool())
	containerNames, ok := tracingAttrValue(span.Attributes, "interlink.pod.container.names")
	require.True(t, ok)
	assert.Equal(t, []string{"worker"}, containerNames.AsStringSlice())
	labelKeys, ok := tracingAttrValue(span.Attributes, "interlink.pod.label.keys")
	require.True(t, ok)
	assert.Equal(t, []string{"team"}, labelKeys.AsStringSlice())
	secretNames, ok := tracingAttrValue(span.Attributes, "interlink.create.secret.names")
	require.True(t, ok)
	assert.Equal(t, []string{"runtime-secret"}, secretNames.AsStringSlice())

	for _, attr := range span.Attributes {
		assert.NotContains(t, fmt.Sprint(attr.Value.AsInterface()), sensitiveValue)
	}
	for _, event := range span.Events {
		for _, attr := range event.Attributes {
			assert.NotContains(t, fmt.Sprint(attr.Value.AsInterface()), sensitiveValue)
		}
	}
}

func TestDefaultTracingOmitsDetailedMetadata(t *testing.T) {
	exporter := tracingSpanRecorder(t)
	h := tracingTestHandler(t, http.StatusOK)
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "span-pod", Namespace: "ns", UID: "span-pod-uid"},
		Spec:       v1.PodSpec{Containers: []v1.Container{{Name: "worker", Image: "private.example/worker:v1"}}},
	}
	h.DeleteHandler(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/delete", tracingJSON(t, pod)))

	span := findTracingSpan(t, exporter, "DeleteAPI")
	_, hasContainerNames := tracingAttrValue(span.Attributes, "interlink.pod.container.names")
	assert.False(t, hasContainerNames)
	for _, attr := range span.Attributes {
		assert.NotContains(t, fmt.Sprint(attr.Value.AsInterface()), "private.example")
	}
}

func (rt *unixSocketRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.HasPrefix(req.URL.Scheme, "http+unix") {
		req.URL.Scheme = "http"
		req.URL.Host = "unix"
	}
	return rt.transport.RoundTrip(req)
}

// newUnixTestServer starts an httptest.Server backed by a unix socket and returns
// the server, a base URL using the http+unix scheme (safe per isSafeURL), and an
// HTTP client that routes requests to that socket.
func newUnixTestServer(t *testing.T, handler http.Handler) (*httptest.Server, string, *http.Client) {
	t.Helper()
	socketPath := filepath.Join(t.TempDir(), "test.sock")
	l, err := net.Listen("unix", socketPath)
	require.NoError(t, err)

	server := httptest.NewUnstartedServer(handler)
	server.Listener = l
	server.Start()

	dialer := &net.Dialer{}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			if strings.HasPrefix(addr, "unix:") {
				return dialer.DialContext(ctx, "unix", socketPath)
			}
			return dialer.DialContext(ctx, "tcp", addr)
		},
	}
	client := &http.Client{Transport: &unixSocketRoundTripper{transport}}

	return server, "http+unix:///", client
}

func TestGetSessionContext(t *testing.T) {
	tests := []struct {
		name           string
		headerValue    string
		expectGenerate bool
	}{
		{
			name:           "existing session context",
			headerValue:    "Request-12345",
			expectGenerate: false,
		},
		{
			name:           "no session context - should generate",
			headerValue:    "",
			expectGenerate: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			if tt.headerValue != "" {
				req.Header.Set("InterLink-Http-Session", tt.headerValue)
			}

			got := GetSessionContext(req)

			if tt.expectGenerate {
				assert.NotEmpty(t, got)
				assert.Contains(t, got, "Request-")
			} else {
				assert.Equal(t, tt.headerValue, got)
			}
		})
	}
}

func TestAddSessionContext(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	sessionID := "Request-test-123"

	AddSessionContext(req, sessionID)

	got := req.Header.Get("InterLink-Http-Session")
	assert.Equal(t, sessionID, got)
}

func TestGetSessionContextMessage(t *testing.T) {
	tests := []struct {
		name           string
		sessionContext string
		expected       string
	}{
		{
			name:           "format session context message",
			sessionContext: "Request-12345",
			expected:       "HTTP InterLink session Request-12345: ",
		},
		{
			name:           "empty session context",
			sessionContext: "",
			expected:       "HTTP InterLink session : ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetSessionContextMessage(tt.sessionContext)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func setupTestTracer() (*trace.TracerProvider, func()) {
	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(
		trace.WithSyncer(exporter),
	)
	otel.SetTracerProvider(tp)

	cleanup := func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			panic(err)
		}
	}

	return tp, cleanup
}

func TestReqWithError_HeadersSet(t *testing.T) {
	tp, cleanup := setupTestTracer()
	defer cleanup()

	tracer := tp.Tracer("test")
	ctx, span := tracer.Start(context.Background(), "test-span")
	defer span.End()

	// Create a test server that echoes back request headers
	testServer, baseURL, client := newUnixTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify headers are set correctly
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.NotEmpty(t, r.Header.Get("InterLink-Http-Session"))

		w.WriteHeader(http.StatusOK)
		if _, err := io.WriteString(w, `{"status":"ok"}`); err != nil {
			panic(err)
		}
	}))
	defer testServer.Close()

	// Create request to test server
	req, err := http.NewRequest(http.MethodGet, baseURL, nil)
	require.NoError(t, err)

	// Create response recorder
	w := httptest.NewRecorder()

	// Call ReqWithError
	sessionContext := "Request-test-123"
	startTime := time.Now().UnixMicro()
	_, err = ReqWithError(
		ctx,
		req,
		w,
		startTime,
		span,
		false,
		true,
		sessionContext,
		client,
	)

	assert.NoError(t, err)
}

func TestReqWithError_ErrorHandling(t *testing.T) {
	tp, cleanup := setupTestTracer()
	defer cleanup()

	tests := []struct {
		name           string
		serverStatus   int
		serverResponse string
		expectError    bool
	}{
		{
			name:           "successful request",
			serverStatus:   http.StatusOK,
			serverResponse: `{"status":"ok"}`,
			expectError:    false,
		},
		{
			name:           "server error",
			serverStatus:   http.StatusInternalServerError,
			serverResponse: "internal server error",
			expectError:    true,
		},
		{
			name:           "bad request",
			serverStatus:   http.StatusBadRequest,
			serverResponse: "bad request",
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tracer := tp.Tracer("test")
			ctx, span := tracer.Start(context.Background(), "test-span")
			defer span.End()

			// Create test server with specific response
			testServer, baseURL, client := newUnixTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.serverStatus)
				if _, err := io.WriteString(w, tt.serverResponse); err != nil {
					panic(err)
				}
			}))
			defer testServer.Close()

			req, err := http.NewRequest(http.MethodGet, baseURL, nil)
			require.NoError(t, err)

			w := httptest.NewRecorder()
			startTime := time.Now().UnixMicro()

			_, err = ReqWithError(
				ctx,
				req,
				w,
				startTime,
				span,
				true,
				true,
				"Request-test",
				client,
			)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestReqWithError_ResponseModes(t *testing.T) {
	tp, cleanup := setupTestTracer()
	defer cleanup()

	testData := `{"test":"data","value":123}`

	tests := []struct {
		name              string
		respondWithValues bool
		respondWithReturn bool
		expectReturnData  bool
		expectWriteData   bool
	}{
		{
			name:              "return and write data",
			respondWithValues: true,
			respondWithReturn: true,
			expectReturnData:  true,
			expectWriteData:   true,
		},
		{
			name:              "only return data",
			respondWithValues: false,
			respondWithReturn: true,
			expectReturnData:  true,
			expectWriteData:   false,
		},
		{
			name:              "only write data (streaming)",
			respondWithValues: true,
			respondWithReturn: false,
			expectReturnData:  false,
			expectWriteData:   true,
		},
		{
			name:              "neither return nor write",
			respondWithValues: false,
			respondWithReturn: false,
			expectReturnData:  false,
			expectWriteData:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tracer := tp.Tracer("test")
			ctx, span := tracer.Start(context.Background(), "test-span")
			defer span.End()

			testServer, baseURL, client := newUnixTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
				if _, err := io.WriteString(w, testData); err != nil {
					panic(err)
				}
			}))
			defer testServer.Close()

			req, err := http.NewRequest(http.MethodGet, baseURL, nil)
			require.NoError(t, err)

			w := httptest.NewRecorder()
			startTime := time.Now().UnixMicro()

			returnedData, err := ReqWithError(
				ctx,
				req,
				w,
				startTime,
				span,
				tt.respondWithValues,
				tt.respondWithReturn,
				"Request-test",
				client,
			)

			require.NoError(t, err)

			if tt.expectReturnData {
				assert.NotNil(t, returnedData)
				assert.Contains(t, string(returnedData), "test")
			} else {
				assert.Nil(t, returnedData)
			}

			if tt.expectWriteData {
				assert.NotEmpty(t, w.Body.String())
			}
		})
	}
}
