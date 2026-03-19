package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

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
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	req, err := http.NewRequest(http.MethodGet, testServer.URL, nil)
	require.NoError(t, err)

	// Create response recorder
	w := httptest.NewRecorder()

	// Create HTTP client
	client := testServer.Client()

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
			testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.serverStatus)
				if _, err := io.WriteString(w, tt.serverResponse); err != nil {
					panic(err)
				}
			}))
			defer testServer.Close()

			req, err := http.NewRequest(http.MethodGet, testServer.URL, nil)
			require.NoError(t, err)

			w := httptest.NewRecorder()
			client := testServer.Client()
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

			testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
				if _, err := io.WriteString(w, testData); err != nil {
					panic(err)
				}
			}))
			defer testServer.Close()

			req, err := http.NewRequest(http.MethodGet, testServer.URL, nil)
			require.NoError(t, err)

			w := httptest.NewRecorder()
			client := testServer.Client()
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
