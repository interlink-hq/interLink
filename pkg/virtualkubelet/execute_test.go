package virtualkubelet

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetSidecarEndpoint(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name         string
		interlinkURL string
		interlinkPort string
		expected     string
	}{
		{
			name:          "HTTP URL",
			interlinkURL:  "http://localhost",
			interlinkPort: "3000",
			expected:      "http://localhost:3000",
		},
		{
			name:          "HTTPS URL",
			interlinkURL:  "https://interlink-api.example.com",
			interlinkPort: "8443",
			expected:      "https://interlink-api.example.com:8443",
		},
		{
			name:          "Unix socket",
			interlinkURL:  "unix:///var/run/interlink.sock",
			interlinkPort: "",
			expected:      "http://unix",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getSidecarEndpoint(ctx, tt.interlinkURL, tt.interlinkPort)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestCreateTLSHTTPClient(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name      string
		tlsConfig TLSConfig
		expectTLS bool
		wantErr   bool
	}{
		{
			name: "TLS disabled",
			tlsConfig: TLSConfig{
				Enabled: false,
			},
			expectTLS: false,
			wantErr:   false,
		},
		{
			name: "TLS enabled without certs",
			tlsConfig: TLSConfig{
				Enabled: true,
			},
			expectTLS: true,
			wantErr:   false,
		},
		{
			name: "TLS enabled with non-existent CA cert",
			tlsConfig: TLSConfig{
				Enabled:    true,
				CACertFile: "/non/existent/ca.crt",
			},
			expectTLS: false,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := createTLSHTTPClient(ctx, tt.tlsConfig)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.NotNil(t, client)

			if !tt.expectTLS {
				// Default client has no custom transport
				assert.Equal(t, http.DefaultClient, client)
			} else {
				// Custom client with TLS transport
				assert.NotEqual(t, http.DefaultClient, client)
			}
		})
	}
}

func TestDoRequestWithClient(t *testing.T) {
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify headers
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		authHeader := r.Header.Get("Authorization")
		if authHeader != "" {
			assert.Contains(t, authHeader, "Bearer")
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer testServer.Close()

	tests := []struct {
		name    string
		token   string
		wantErr bool
	}{
		{
			name:    "request without token",
			token:   "",
			wantErr: false,
		},
		{
			name:    "request with token",
			token:   "test-token-123",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, testServer.URL, nil)
			require.NoError(t, err)

			client := testServer.Client()
			resp, err := doRequestWithClient(req, tt.token, client)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.NotNil(t, resp)
			assert.Equal(t, http.StatusOK, resp.StatusCode)
			resp.Body.Close()
		})
	}
}

func TestAddSessionContext(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	sessionID := "test-session-123"

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
			name:           "normal session context",
			sessionContext: "CreatePod#12345",
			expected:       "HTTP InterLink session CreatePod#12345: ",
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
