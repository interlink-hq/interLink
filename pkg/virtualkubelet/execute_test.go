package virtualkubelet

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	types "github.com/interlink-hq/interlink/pkg/interlink"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	authenticationv1 "k8s.io/api/authentication/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

const testNamespace = "test-ns"

// Literals shared by the tests of this package. They live in one block so each
// value is written once; where the production code already names a value
// (valueTrue, nvidiaGPU, virtualNodeNoScheduleTaint) the tests reuse that
// constant instead of adding a second name for it.
const (
	// Object names.
	testPodName        = "test-pod"
	testPodDefaultName = "pod-default"
	testShadowName     = "shadow-nb"
	testAppName        = "app"

	// ConfigMap / secret keys and values.
	testConfigMapName       = "my-config"
	testConfigKey1          = "key1"
	testConfigKey2          = "key2"
	testCACrtKey            = "ca.crt"
	testOverrideCert        = "OVERRIDE-CERT"
	testLogLevelEnv         = "LOG_LEVEL"
	testSSHKeySecret        = "hpc-ssh-key"
	testKnownHostsConfigMap = "hpc-known-hosts"
	testConfigValue1        = "value1"
	testConfigValue2        = "value2"

	// Downward API field paths and projected volume entries.
	testFieldPathMetadataName        = "metadata.name"
	testFieldPathMetadataLabels      = "metadata.labels"
	testFieldPathMetadataAnnotations = "metadata.annotations"
	testPodNamePath                  = "pod-name"
	testLabelsPath                   = "labels"
	testAnnotationsPath              = "annotations"
	testAnnotationValue              = "value"

	// Node capacity, accelerator and taint values.
	testGPUModel             = "A100"
	testMemoryQuantity       = "32Gi"
	testMilliQuantity        = "500m"
	testTaintEffectNoExecute = "NoExecute"
	testTaintKeyExisting     = "existing"
	testTaintKeyPlugin       = "plugin"
	testTaintValueNew        = "new"
	testTaintValueOld        = "old"

	// Status and annotation values.
	testConditionTrue     = "True"
	testMeshDisabledValue = "disabled"
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
		{name: "localhost http", rawurl: "http://localhost/path", expected: true},
		{name: "127.0.0.1 http", rawurl: "http://127.0.0.1/path", expected: true},
		{name: "::1 http", rawurl: "http://[::1]/path", expected: true},
		{name: ".internal domain", rawurl: "http://service.internal/path", expected: true},
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

func TestDeleteRequestHonoursContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		// The request body has to be drained before waiting. net/http starts the
		// background read that notices a disconnected client only once the body has
		// been consumed, so a handler blocking on an unread body never sees its
		// context cancelled, never returns, and the server.Close() below waits for
		// it until the whole package times out.
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			t.Errorf("draining the request body failed: %v", err)
		}

		select {
		case <-r.Context().Done():
		// Never reached unless the cancellation stops propagating again: it is here
		// so that such a regression fails this test instead of hanging the suite.
		case <-time.After(10 * time.Second):
		}
	}))
	defer server.Close()

	host, port, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	require.NoError(t, err)

	pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{UID: "test-pod-uid"}}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	_, err = deleteRequest(ctx, Config{
		InterlinkURL:  "http://" + host,
		InterlinkPort: port,
	}, pod, "")
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
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

func TestRemoteExecutionHandleProjectedSourceServiceAccountToken(t *testing.T) {
	const testTokenPath = "token"
	ctx := context.Background()
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testPodName,
			Namespace: testNamespace,
			UID:       "token-pod-uid",
		},
		Spec: v1.PodSpec{
			ServiceAccountName: "svc-account",
		},
	}

	tests := []struct {
		name              string
		audience          string
		expectedAudiences []string
	}{
		{
			name:              "no audience requests the API server default audiences",
			audience:          "",
			expectedAudiences: nil,
		},
		{
			name:              "audience is requested for the token",
			audience:          "vault",
			expectedAudiences: []string{"vault"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewSimpleClientset()
			var requested *authenticationv1.TokenRequest
			fakeClient.PrependReactor("create", "serviceaccounts", func(action k8stesting.Action) (bool, runtime.Object, error) {
				createAction, ok := action.(k8stesting.CreateAction)
				if !ok || createAction.GetSubresource() != "token" {
					return false, nil, nil
				}
				requested = createAction.GetObject().(*authenticationv1.TokenRequest).DeepCopy()
				response := requested.DeepCopy()
				response.Status.Token = "token-for-" + strings.Join(requested.Spec.Audiences, ",")
				return true, response, nil
			})

			source := v1.VolumeProjection{
				ServiceAccountToken: &v1.ServiceAccountTokenProjection{
					Audience: tt.audience,
					Path:     testTokenPath,
				},
			}
			projectedVolume := &v1.ConfigMap{Data: map[string]string{}}

			err := remoteExecutionHandleProjectedSource(ctx, &Provider{clientSet: fakeClient}, pod, source, projectedVolume)
			require.NoError(t, err)

			require.NotNil(t, requested)
			assert.Equal(t, tt.expectedAudiences, requested.Spec.Audiences)
			require.NotNil(t, requested.Spec.BoundObjectRef)
			assert.Equal(t, "Pod", requested.Spec.BoundObjectRef.Kind)
			assert.Equal(t, pod.UID, requested.Spec.BoundObjectRef.UID)
			assert.Equal(t, "token-for-"+tt.audience, projectedVolume.Data[testTokenPath])
		})
	}
}

func TestRemoteExecutionHandleProjectedSourceConfigMap(t *testing.T) {
	ctx := context.Background()
	namespace := testNamespace

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testPodName,
			Namespace: namespace,
		},
	}

	tests := []struct {
		name          string
		configMapName string
		configMapData map[string]string
		sourceItems   []v1.KeyToPath
		overrideCaCrt string
		optional      bool
		expectedData  map[string]string
		expectErr     bool
	}{
		{
			name:          "configmap without items projects all keys",
			configMapName: testConfigMapName,
			configMapData: map[string]string{
				testConfigKey1: testConfigValue1,
				testConfigKey2: testConfigValue2,
			},
			sourceItems:   nil,
			overrideCaCrt: "",
			expectedData: map[string]string{
				testConfigKey1: testConfigValue1,
				testConfigKey2: testConfigValue2,
			},
		},
		{
			name:          "configmap with items projects only specified keys",
			configMapName: testConfigMapName,
			configMapData: map[string]string{
				testConfigKey1: testConfigValue1,
				testConfigKey2: testConfigValue2,
				"key3":         "value3",
			},
			sourceItems: []v1.KeyToPath{
				{Key: testConfigKey1, Path: "mapped-key1"},
				{Key: testConfigKey2, Path: "mapped-key2"},
			},
			overrideCaCrt: "",
			expectedData: map[string]string{
				"mapped-key1": testConfigValue1,
				"mapped-key2": testConfigValue2,
			},
		},
		{
			name:          "kube-root-ca.crt without items uses override and default path",
			configMapName: "kube-root-ca.crt",
			configMapData: nil,
			sourceItems:   nil,
			overrideCaCrt: testOverrideCert,
			expectedData: map[string]string{
				testCACrtKey: testOverrideCert,
			},
		},
		{
			name:          "kube-root-ca.crt with items uses override value at specified paths",
			configMapName: "kube-root-ca.crt",
			configMapData: nil,
			sourceItems: []v1.KeyToPath{
				{Key: testCACrtKey, Path: testCACrtKey},
			},
			overrideCaCrt: testOverrideCert,
			expectedData: map[string]string{
				testCACrtKey: testOverrideCert,
			},
		},
		{
			name:          "configmap without items with multiline value preserves newlines",
			configMapName: testConfigMapName,
			configMapData: map[string]string{
				testCACrtKey: "-----BEGIN CERTIFICATE-----\nMIIBIjAN\n-----END CERTIFICATE-----\n",
			},
			sourceItems:   nil,
			overrideCaCrt: "",
			expectedData: map[string]string{
				testCACrtKey: "-----BEGIN CERTIFICATE-----\nMIIBIjAN\n-----END CERTIFICATE-----\n",
			},
		},
		{
			name:          "missing key in items returns error",
			configMapName: testConfigMapName,
			configMapData: map[string]string{
				testConfigKey1: testConfigValue1,
			},
			sourceItems: []v1.KeyToPath{
				{Key: "missing-key", Path: "some-path"},
			},
			overrideCaCrt: "",
			expectedData:  nil,
			expectErr:     true,
		},
		{
			name:          "missing key in items of optional configmap is skipped",
			configMapName: testConfigMapName,
			configMapData: map[string]string{
				testConfigKey1: testConfigValue1,
			},
			sourceItems: []v1.KeyToPath{
				{Key: testConfigKey1, Path: testConfigKey1},
				{Key: "missing-key", Path: "some-path"},
			},
			optional: true,
			expectedData: map[string]string{
				testConfigKey1: testConfigValue1,
			},
		},
		{
			name:          "missing configmap returns error",
			configMapName: testConfigMapName,
			configMapData: nil,
			expectedData:  nil,
			expectErr:     true,
		},
		{
			name:          "missing optional configmap projects no keys",
			configMapName: testConfigMapName,
			configMapData: nil,
			optional:      true,
			expectedData:  map[string]string{},
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

			optional := tt.optional
			source := v1.VolumeProjection{
				ConfigMap: &v1.ConfigMapProjection{
					LocalObjectReference: v1.LocalObjectReference{Name: tt.configMapName},
					Items:                tt.sourceItems,
					Optional:             &optional,
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

func TestRemoteExecutionHandleProjectedSourceDownwardAPIFieldRef(t *testing.T) {
	ctx := context.Background()
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        testPodName,
			Namespace:   testNamespace,
			UID:         "uid-1234",
			Labels:      map[string]string{testAppName: "demo", "tier": "backend"},
			Annotations: map[string]string{"my.annotation/key": testAnnotationValue},
		},
		Spec: v1.PodSpec{
			NodeName:           "node-a",
			ServiceAccountName: "svc-account",
		},
		Status: v1.PodStatus{
			PodIP:  "10.42.0.15",
			HostIP: "172.18.0.20",
		},
	}
	source := v1.VolumeProjection{
		DownwardAPI: &v1.DownwardAPIProjection{
			Items: []v1.DownwardAPIVolumeFile{
				{Path: testPodNamePath, FieldRef: &v1.ObjectFieldSelector{FieldPath: testFieldPathMetadataName}},
				{Path: "namespace", FieldRef: &v1.ObjectFieldSelector{FieldPath: "metadata.namespace"}},
				{Path: "uid", FieldRef: &v1.ObjectFieldSelector{FieldPath: "metadata.uid"}},
				{Path: testLabelsPath, FieldRef: &v1.ObjectFieldSelector{FieldPath: testFieldPathMetadataLabels}},
				{Path: testAnnotationsPath, FieldRef: &v1.ObjectFieldSelector{FieldPath: testFieldPathMetadataAnnotations}},
				{Path: "node-name", FieldRef: &v1.ObjectFieldSelector{FieldPath: "spec.nodeName"}},
				{Path: "sa-name", FieldRef: &v1.ObjectFieldSelector{FieldPath: "spec.serviceAccountName"}},
				{Path: "pod-ip", FieldRef: &v1.ObjectFieldSelector{FieldPath: "status.podIP"}},
				{Path: "host-ip", FieldRef: &v1.ObjectFieldSelector{FieldPath: "status.hostIP"}},
				{Path: "tier", FieldRef: &v1.ObjectFieldSelector{FieldPath: "metadata.labels['tier']"}},
				{Path: "annotation", FieldRef: &v1.ObjectFieldSelector{FieldPath: "metadata.annotations['my.annotation/key']"}},
			},
		},
	}
	projectedVolume := &v1.ConfigMap{Data: map[string]string{}}

	err := remoteExecutionHandleProjectedSource(ctx, &Provider{}, pod, source, projectedVolume)
	require.NoError(t, err)

	assert.Equal(t, testPodName, projectedVolume.Data[testPodNamePath])
	assert.Equal(t, testNamespace, projectedVolume.Data["namespace"])
	assert.Equal(t, "uid-1234", projectedVolume.Data["uid"])
	assert.Equal(t, "app=\"demo\"\ntier=\"backend\"\n", projectedVolume.Data[testLabelsPath])
	assert.Equal(t, "my.annotation/key=\"value\"\n", projectedVolume.Data[testAnnotationsPath])
	assert.Equal(t, "node-a", projectedVolume.Data["node-name"])
	assert.Equal(t, "svc-account", projectedVolume.Data["sa-name"])
	assert.Equal(t, "10.42.0.15", projectedVolume.Data["pod-ip"])
	assert.Equal(t, "172.18.0.20", projectedVolume.Data["host-ip"])
	assert.Equal(t, "backend", projectedVolume.Data["tier"])
	assert.Equal(t, testAnnotationValue, projectedVolume.Data["annotation"])
}

func TestRemoteExecutionHandleVolumesDownwardAPI(t *testing.T) {
	ctx := context.Background()
	namespace := testNamespace
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        testPodName,
			Namespace:   namespace,
			UID:         "uid-1234",
			Labels:      map[string]string{testAppName: "demo"},
			Annotations: map[string]string{"a": "b"},
		},
		Spec: v1.PodSpec{
			Volumes: []v1.Volume{
				{
					Name: "podinfo",
					VolumeSource: v1.VolumeSource{
						DownwardAPI: &v1.DownwardAPIVolumeSource{
							Items: []v1.DownwardAPIVolumeFile{
								{Path: testPodNamePath, FieldRef: &v1.ObjectFieldSelector{FieldPath: testFieldPathMetadataName}},
								{Path: testLabelsPath, FieldRef: &v1.ObjectFieldSelector{FieldPath: testFieldPathMetadataLabels}},
								{Path: testAnnotationsPath, FieldRef: &v1.ObjectFieldSelector{FieldPath: testFieldPathMetadataAnnotations}},
							},
						},
					},
				},
			},
		},
	}

	fakeClient := fake.NewSimpleClientset(pod.DeepCopy())
	p := &Provider{
		clientSet: fakeClient,
		notifier:  func(*v1.Pod) {},
	}
	req := &types.PodCreateRequests{}

	err := remoteExecutionHandleVolumes(ctx, p, pod, req)
	require.NoError(t, err)
	require.Len(t, req.ProjectedVolumeMaps, 1)
	assert.Equal(t, "podinfo", req.ProjectedVolumeMaps[0].Name)
	assert.Equal(t, testPodName, req.ProjectedVolumeMaps[0].Data[testPodNamePath])
	assert.Equal(t, "app=\"demo\"\n", req.ProjectedVolumeMaps[0].Data[testLabelsPath])
	assert.Equal(t, "a=\"b\"\n", req.ProjectedVolumeMaps[0].Data[testAnnotationsPath])
	require.Len(t, pod.Spec.Volumes, 1)
	require.NotNil(t, pod.Spec.Volumes[0].Projected)
	assert.Nil(t, pod.Spec.Volumes[0].DownwardAPI)
	require.Len(t, pod.Spec.Volumes[0].Projected.Sources, 1)
	require.NotNil(t, pod.Spec.Volumes[0].Projected.Sources[0].DownwardAPI)
	assert.Equal(t, pod.Spec.Volumes[0].Projected.Sources[0].DownwardAPI.Items, []v1.DownwardAPIVolumeFile{
		{Path: testPodNamePath, FieldRef: &v1.ObjectFieldSelector{FieldPath: testFieldPathMetadataName}},
		{Path: testLabelsPath, FieldRef: &v1.ObjectFieldSelector{FieldPath: testFieldPathMetadataLabels}},
		{Path: testAnnotationsPath, FieldRef: &v1.ObjectFieldSelector{FieldPath: testFieldPathMetadataAnnotations}},
	})
}

func TestRemoteExecutionHandleVolumesDownwardAPIDisabledProjectedVolumes(t *testing.T) {
	ctx := context.Background()
	namespace := testNamespace
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testPodName,
			Namespace: namespace,
		},
		Spec: v1.PodSpec{
			Volumes: []v1.Volume{
				{
					Name: "podinfo",
					VolumeSource: v1.VolumeSource{
						DownwardAPI: &v1.DownwardAPIVolumeSource{
							Items: []v1.DownwardAPIVolumeFile{
								{Path: testPodNamePath, FieldRef: &v1.ObjectFieldSelector{FieldPath: testFieldPathMetadataName}},
							},
						},
					},
				},
			},
		},
	}

	fakeClient := fake.NewSimpleClientset(pod.DeepCopy())
	p := &Provider{
		clientSet: fakeClient,
		notifier:  func(*v1.Pod) {},
		config: Config{
			DisableProjectedVolumes: true,
		},
	}
	req := &types.PodCreateRequests{}

	err := remoteExecutionHandleVolumes(ctx, p, pod, req)
	require.NoError(t, err)
	assert.Empty(t, req.ProjectedVolumeMaps)
	require.Len(t, pod.Spec.Volumes, 1)
	require.NotNil(t, pod.Spec.Volumes[0].DownwardAPI)
	assert.Nil(t, pod.Spec.Volumes[0].Projected)
}

func TestRemoteExecutionHandleVolumesOptionalMissing(t *testing.T) {
	const (
		missingConfigMap = "missing-config"
		missingSecret    = "missing-secret"
	)
	ctx := context.Background()
	namespace := testNamespace
	optional := true
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testPodName,
			Namespace: namespace,
		},
		Spec: v1.PodSpec{
			Volumes: []v1.Volume{
				{
					Name: "config",
					VolumeSource: v1.VolumeSource{
						ConfigMap: &v1.ConfigMapVolumeSource{
							LocalObjectReference: v1.LocalObjectReference{Name: missingConfigMap},
							Optional:             &optional,
						},
					},
				},
				{
					Name: "secret",
					VolumeSource: v1.VolumeSource{
						Secret: &v1.SecretVolumeSource{
							SecretName: missingSecret,
							Optional:   &optional,
						},
					},
				},
			},
		},
	}

	fakeClient := fake.NewSimpleClientset(pod.DeepCopy())
	p := &Provider{
		clientSet: fakeClient,
		notifier:  func(*v1.Pod) {},
	}
	req := &types.PodCreateRequests{}

	// Without the optional handling this waits five minutes for the objects to appear.
	err := remoteExecutionHandleVolumes(ctx, p, pod, req)
	require.NoError(t, err)
	assert.Equal(t, v1.PodPending, pod.Status.Phase)
	require.Len(t, req.ConfigMaps, 1)
	assert.Equal(t, missingConfigMap, req.ConfigMaps[0].Name)
	assert.Equal(t, namespace, req.ConfigMaps[0].Namespace)
	assert.Empty(t, req.ConfigMaps[0].Data)
	require.Len(t, req.Secrets, 1)
	assert.Equal(t, missingSecret, req.Secrets[0].Name)
	assert.Equal(t, namespace, req.Secrets[0].Namespace)
	assert.Empty(t, req.Secrets[0].Data)
}

func TestResolveEnvFromRefs(t *testing.T) {
	ctx := context.Background()
	namespace := testNamespace

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testPodName,
			Namespace: namespace,
		},
	}

	container := &v1.Container{
		Name: deleteTestContainer,
		Env: []v1.EnvVar{
			{Name: testLogLevelEnv, Value: "info"},
		},
		EnvFrom: []v1.EnvFromSource{
			{
				ConfigMapRef: &v1.ConfigMapEnvSource{
					LocalObjectReference: v1.LocalObjectReference{Name: "app-config"},
				},
			},
			{
				SecretRef: &v1.SecretEnvSource{
					LocalObjectReference: v1.LocalObjectReference{Name: "aws-credentials"},
				},
				Prefix: "AWS_",
			},
		},
	}

	fakeClient := fake.NewSimpleClientset(
		&v1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "app-config", Namespace: namespace},
			Data: map[string]string{
				testLogLevelEnv: "debug",
				"DATABASE":      "postgresql",
			},
		},
		&v1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "aws-credentials", Namespace: namespace},
			Data: map[string][]byte{
				"ACCESS_KEY_ID": []byte("AKIA"),
			},
		},
	)

	p := &Provider{
		clientSet: fakeClient,
	}

	resolveEnvFromRefs(ctx, p, pod, container)

	assert.Empty(t, container.EnvFrom)
	assert.Contains(t, container.Env, v1.EnvVar{Name: testLogLevelEnv, Value: "info"})
	assert.Contains(t, container.Env, v1.EnvVar{Name: "DATABASE", Value: "postgresql"})
	assert.Contains(t, container.Env, v1.EnvVar{Name: "AWS_ACCESS_KEY_ID", Value: "AKIA"})
}

func TestResolveEnvRefsFieldRef(t *testing.T) {
	const (
		podUID   = "uid-env"
		appLabel = "frontend"
		team     = "hpc"
		nodeName = "vk-node"
		account  = "runner"
		podIP    = "10.42.0.16"
		hostIP   = "172.18.0.21"
	)
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        testPodName,
			Namespace:   testNamespace,
			UID:         podUID,
			Labels:      map[string]string{testAppName: appLabel},
			Annotations: map[string]string{"team": team},
		},
		Spec: v1.PodSpec{
			NodeName:           nodeName,
			ServiceAccountName: account,
		},
		Status: v1.PodStatus{
			PodIP:  podIP,
			HostIP: hostIP,
		},
	}

	fieldRef := func(name, fieldPath string) v1.EnvVar {
		return v1.EnvVar{
			Name:      name,
			ValueFrom: &v1.EnvVarSource{FieldRef: &v1.ObjectFieldSelector{FieldPath: fieldPath}},
		}
	}
	container := &v1.Container{
		Name: deleteTestContainer,
		Env: []v1.EnvVar{
			fieldRef("POD_NAME", testFieldPathMetadataName),
			fieldRef("POD_NAMESPACE", "metadata.namespace"),
			fieldRef("POD_UID", "metadata.uid"),
			fieldRef("APP", "metadata.labels['app']"),
			fieldRef("TEAM", "metadata.annotations['team']"),
			fieldRef("MISSING_ANNOTATION", "metadata.annotations['absent']"),
			fieldRef("NODE_NAME", "spec.nodeName"),
			fieldRef("SERVICE_ACCOUNT", "spec.serviceAccountName"),
			fieldRef("POD_IP", "status.podIP"),
			fieldRef("POD_IPS", "status.podIPs"),
			fieldRef("HOST_IP", "status.hostIP"),
			fieldRef("HOST_IPS", "status.hostIPs"),
			fieldRef("UNSUPPORTED", "spec.unsupported"),
		},
	}

	resolveEnvRefs(context.Background(), &Provider{}, pod, container)

	expected := map[string]string{
		"POD_NAME":           testPodName,
		"POD_NAMESPACE":      testNamespace,
		"POD_UID":            podUID,
		"APP":                appLabel,
		"TEAM":               team,
		"MISSING_ANNOTATION": "",
		"NODE_NAME":          nodeName,
		"SERVICE_ACCOUNT":    account,
		"POD_IP":             podIP,
		"POD_IPS":            podIP,
		"HOST_IP":            hostIP,
		"HOST_IPS":           hostIP,
	}
	for _, env := range container.Env {
		if env.Name == "UNSUPPORTED" {
			assert.NotNil(t, env.ValueFrom, "an unsupported field path is left unresolved")
			continue
		}
		assert.Nil(t, env.ValueFrom, env.Name)
		assert.Equal(t, expected[env.Name], env.Value, env.Name)
	}
}

func TestResolveEnvFromRefsOptionalMissingSecret(t *testing.T) {
	ctx := context.Background()
	namespace := testNamespace

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testPodName,
			Namespace: namespace,
		},
	}

	optional := true
	container := &v1.Container{
		Name: deleteTestContainer,
		EnvFrom: []v1.EnvFromSource{
			{
				SecretRef: &v1.SecretEnvSource{
					LocalObjectReference: v1.LocalObjectReference{Name: "missing-secret"},
					Optional:             &optional,
				},
			},
		},
	}

	p := &Provider{
		clientSet: fake.NewSimpleClientset(),
	}

	resolveEnvFromRefs(ctx, p, pod, container)

	assert.Empty(t, container.Env)
	assert.Empty(t, container.EnvFrom)
}
