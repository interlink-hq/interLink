package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	types "github.com/interlink-hq/interlink/pkg/interlink"
)

func setupLogsTestTracer() (*trace.TracerProvider, func()) {
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

func TestValidateLogRequest(t *testing.T) {
	tests := []struct {
		name      string
		req       types.LogStruct
		wantErr   bool
		errSubstr string
	}{
		{
			name: "valid request",
			req: types.LogStruct{
				Namespace:     "default",
				PodUID:        "12345678-1234-1234-1234-123456789012",
				ContainerName: "my-container",
			},
			wantErr: false,
		},
		{
			name: "valid request without container name",
			req: types.LogStruct{
				Namespace: "kube-system",
				PodUID:    "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
			},
			wantErr: false,
		},
		{
			name: "single character namespace",
			req: types.LogStruct{
				Namespace: "a",
				PodUID:    "12345678-1234-1234-1234-123456789012",
			},
			wantErr: false,
		},
		{
			name: "invalid namespace - contains path separator",
			req: types.LogStruct{
				Namespace: "default/../../etc",
				PodUID:    "12345678-1234-1234-1234-123456789012",
			},
			wantErr:   true,
			errSubstr: "invalid namespace",
		},
		{
			name: "invalid namespace - contains uppercase",
			req: types.LogStruct{
				Namespace: "Default",
				PodUID:    "12345678-1234-1234-1234-123456789012",
			},
			wantErr:   true,
			errSubstr: "invalid namespace",
		},
		{
			name: "invalid namespace - starts with hyphen",
			req: types.LogStruct{
				Namespace: "-default",
				PodUID:    "12345678-1234-1234-1234-123456789012",
			},
			wantErr:   true,
			errSubstr: "invalid namespace",
		},
		{
			name: "invalid pod UID - not UUID format",
			req: types.LogStruct{
				Namespace: "default",
				PodUID:    "not-a-uuid",
			},
			wantErr:   true,
			errSubstr: "invalid pod UID",
		},
		{
			name: "invalid pod UID - path traversal",
			req: types.LogStruct{
				Namespace: "default",
				PodUID:    "../../etc/passwd",
			},
			wantErr:   true,
			errSubstr: "invalid pod UID",
		},
		{
			name: "invalid container name - contains path separator",
			req: types.LogStruct{
				Namespace:     "default",
				PodUID:        "12345678-1234-1234-1234-123456789012",
				ContainerName: "container/../../../etc/passwd",
			},
			wantErr:   true,
			errSubstr: "invalid container name",
		},
		{
			name: "invalid container name - contains uppercase",
			req: types.LogStruct{
				Namespace:     "default",
				PodUID:        "12345678-1234-1234-1234-123456789012",
				ContainerName: "MyContainer",
			},
			wantErr:   true,
			errSubstr: "invalid container name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateLogRequest(tt.req)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGetLogsHandler_Validation(t *testing.T) {
	_, cleanup := setupLogsTestTracer()
	defer cleanup()

	// Create a mock sidecar that returns OK
	sidecar := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("log output"))
	}))
	defer sidecar.Close()

	handler := &InterLinkHandler{
		Ctx:             context.Background(),
		SidecarEndpoint: sidecar.URL,
		ClientHTTP:      sidecar.Client(),
	}

	tests := []struct {
		name           string
		req            types.LogStruct
		expectedStatus int
		expectedBody   string
	}{
		{
			name: "valid request forwarded to sidecar",
			req: types.LogStruct{
				Namespace:     "default",
				PodUID:        "12345678-1234-1234-1234-123456789012",
				ContainerName: "my-container",
				PodName:       "my-pod",
			},
			expectedStatus: http.StatusOK,
		},
		{
			name: "invalid namespace rejected",
			req: types.LogStruct{
				Namespace:     "../etc",
				PodUID:        "12345678-1234-1234-1234-123456789012",
				ContainerName: "my-container",
			},
			expectedStatus: http.StatusBadRequest,
			expectedBody:   "invalid namespace",
		},
		{
			name: "invalid pod UID rejected",
			req: types.LogStruct{
				Namespace:     "default",
				PodUID:        "../../etc/passwd",
				ContainerName: "my-container",
			},
			expectedStatus: http.StatusBadRequest,
			expectedBody:   "invalid pod UID",
		},
		{
			name: "invalid container name rejected",
			req: types.LogStruct{
				Namespace:     "default",
				PodUID:        "12345678-1234-1234-1234-123456789012",
				ContainerName: "../../../etc",
			},
			expectedStatus: http.StatusBadRequest,
			expectedBody:   "invalid container name",
		},
		{
			name: "both Tail and LimitBytes set",
			req: types.LogStruct{
				Namespace: "default",
				PodUID:    "12345678-1234-1234-1234-123456789012",
				Opts: types.ContainerLogOpts{
					Tail:       10,
					LimitBytes: 1024,
				},
			},
			expectedStatus: http.StatusBadRequest,
			expectedBody:   "Both Tail and LimitBytes set",
		},
		{
			name: "both SinceSeconds and SinceTime set",
			req: types.LogStruct{
				Namespace: "default",
				PodUID:    "12345678-1234-1234-1234-123456789012",
				Opts: types.ContainerLogOpts{
					SinceSeconds: 60,
					SinceTime:    time.Now(),
				},
			},
			expectedStatus: http.StatusBadRequest,
			expectedBody:   "Both SinceSeconds and SinceTime set",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := json.Marshal(tt.req)
			require.NoError(t, err)

			r := httptest.NewRequest(http.MethodGet, "/getLogs", bytes.NewReader(body))
			w := httptest.NewRecorder()

			handler.GetLogsHandler(w, r)

			assert.Equal(t, tt.expectedStatus, w.Code)
			if tt.expectedBody != "" {
				assert.Contains(t, w.Body.String(), tt.expectedBody)
			}
		})
	}
}

func TestIsSafeURL(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		expected bool
	}{
		{
			name:     "valid http URL",
			url:      "http://example.com/path",
			expected: true,
		},
		{
			name:     "valid https URL",
			url:      "https://example.com/path",
			expected: true,
		},
		{
			name:     "localhost http allowed (valid sidecar endpoint)",
			url:      "http://localhost:8080",
			expected: true,
		},
		{
			name:     "127.0.0.1 http allowed (valid sidecar endpoint)",
			url:      "http://127.0.0.1:8080",
			expected: true,
		},
		{
			name:     "file scheme blocked",
			url:      "file:///etc/passwd",
			expected: false,
		},
		{
			name:     "ftp scheme blocked",
			url:      "ftp://example.com",
			expected: false,
		},
		{
			name:     "invalid URL blocked",
			url:      "not-a-url",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isSafeURL(tt.url)
			assert.Equal(t, tt.expected, result)
		})
	}
}
