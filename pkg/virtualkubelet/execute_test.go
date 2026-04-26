package virtualkubelet

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// unixSocketRoundTripper rewrites http+unix URLs to http://unix so the underlying
// transport can dial the configured unix socket.
type unixSocketRoundTripper struct {
	transport http.RoundTripper
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

func TestIsSafeURL(t *testing.T) {
	tests := []struct {
		name     string
		rawurl   string
		expected bool
	}{
		{name: "http URL", rawurl: "http://example.com/path", expected: true},
		{name: "https URL", rawurl: "https://example.com/path", expected: true},
		{name: "http+unix URL", rawurl: "http+unix:///var/run/plugin.sock:/status", expected: true},
		{name: "ftp URL", rawurl: "ftp://example.com", expected: false},
		{name: "invalid URL", rawurl: "://bad", expected: false},
		{name: "localhost http", rawurl: "http://localhost/path", expected: false},
		{name: "127.0.0.1 http", rawurl: "http://127.0.0.1/path", expected: false},
		{name: "::1 http", rawurl: "http://[::1]/path", expected: false},
		{name: ".internal domain", rawurl: "http://service.internal/path", expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isSafeURL(tt.rawurl)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestGetSidecarEndpoint(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name          string
		interlinkURL  string
		interlinkPort string
		expected      string
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
	testServer, baseURL, client := newUnixTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify headers
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		authHeader := r.Header.Get("Authorization")
		if authHeader != "" {
			assert.Contains(t, authHeader, "Bearer")
		}

		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(`{"status":"ok"}`)); err != nil {
			panic(err)
		}
	}))
	defer testServer.Close()

	// Allow loopback URLs for the test server
	origChecker := urlSafetyChecker
	urlSafetyChecker = func(string) bool { return true }
	defer func() { urlSafetyChecker = origChecker }()

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
			req, err := http.NewRequest(http.MethodGet, baseURL, nil)
			require.NoError(t, err)

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

func TestRemoteExecutionHandleProjectedSourceConfigMap(t *testing.T) {
	ctx := context.Background()
	namespace := "test-ns"

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: namespace,
		},
	}

	tests := []struct {
		name          string
		configMapName string
		configMapData map[string]string
		sourceItems   []v1.KeyToPath
		overrideCaCrt string
		expectedData  map[string]string
		expectErr     bool
	}{
		{
			name:          "configmap without items projects all keys",
			configMapName: "my-config",
			configMapData: map[string]string{
				"key1": "value1",
				"key2": "value2",
			},
			sourceItems:   nil,
			overrideCaCrt: "",
			expectedData: map[string]string{
				"key1": "value1",
				"key2": "value2",
			},
		},
		{
			name:          "configmap with items projects only specified keys",
			configMapName: "my-config",
			configMapData: map[string]string{
				"key1": "value1",
				"key2": "value2",
				"key3": "value3",
			},
			sourceItems: []v1.KeyToPath{
				{Key: "key1", Path: "mapped-key1"},
				{Key: "key2", Path: "mapped-key2"},
			},
			overrideCaCrt: "",
			expectedData: map[string]string{
				"mapped-key1": "value1",
				"mapped-key2": "value2",
			},
		},
		{
			name:          "kube-root-ca.crt without items uses override and default path",
			configMapName: "kube-root-ca.crt",
			configMapData: nil,
			sourceItems:   nil,
			overrideCaCrt: "OVERRIDE-CERT",
			expectedData: map[string]string{
				"ca.crt": "OVERRIDE-CERT",
			},
		},
		{
			name:          "kube-root-ca.crt with items uses override value at specified paths",
			configMapName: "kube-root-ca.crt",
			configMapData: nil,
			sourceItems: []v1.KeyToPath{
				{Key: "ca.crt", Path: "ca.crt"},
			},
			overrideCaCrt: "OVERRIDE-CERT",
			expectedData: map[string]string{
				"ca.crt": "OVERRIDE-CERT",
			},
		},
		{
			name:          "configmap without items with multiline value preserves newlines",
			configMapName: "my-config",
			configMapData: map[string]string{
				"ca.crt": "-----BEGIN CERTIFICATE-----\nMIIBIjAN\n-----END CERTIFICATE-----\n",
			},
			sourceItems:   nil,
			overrideCaCrt: "",
			expectedData: map[string]string{
				"ca.crt": "-----BEGIN CERTIFICATE-----\nMIIBIjAN\n-----END CERTIFICATE-----\n",
			},
		},
		{
			name:          "missing key in items returns error",
			configMapName: "my-config",
			configMapData: map[string]string{
				"key1": "value1",
			},
			sourceItems: []v1.KeyToPath{
				{Key: "missing-key", Path: "some-path"},
			},
			overrideCaCrt: "",
			expectedData:  nil,
			expectErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a fake k8s client and pre-populate it with the ConfigMap.
			fakeClient := fake.NewSimpleClientset()
			if tt.configMapData != nil {
				cm := &v1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      tt.configMapName,
						Namespace: namespace,
					},
					Data: tt.configMapData,
				}
				_, err := fakeClient.CoreV1().ConfigMaps(namespace).Create(ctx, cm, metav1.CreateOptions{})
				require.NoError(t, err)
			}

			p := &Provider{
				clientSet: fakeClient,
				config: Config{
					KubernetesAPICaCrt: tt.overrideCaCrt,
				},
			}

			source := v1.VolumeProjection{
				ConfigMap: &v1.ConfigMapProjection{
					LocalObjectReference: v1.LocalObjectReference{Name: tt.configMapName},
					Items:                tt.sourceItems,
				},
			}

			projectedVolume := &v1.ConfigMap{
				Data: make(map[string]string),
			}

			err := remoteExecutionHandleProjectedSource(ctx, p, pod, source, projectedVolume)

			if tt.expectErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expectedData, projectedVolume.Data)
		})
	}
}
