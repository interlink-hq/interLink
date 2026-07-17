package virtualkubelet

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func newDeleteTestProvider(t *testing.T, handler http.Handler) (*Provider, *v1.Pod) {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	serverURL, err := url.Parse(server.URL)
	require.NoError(t, err)
	host, port, err := net.SplitHostPort(serverURL.Host)
	require.NoError(t, err)

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: testNamespace,
			UID:       "test-pod-uid",
		},
		Status: v1.PodStatus{Phase: v1.PodRunning},
	}

	provider := &Provider{
		config: Config{
			InterlinkURL:  "http://" + host,
			InterlinkPort: port,
		},
		pods:     map[string]*v1.Pod{string(pod.UID): pod},
		notifier: func(*v1.Pod) {},
	}

	return provider, pod
}

func podIsTracked(provider *Provider, uid string) bool {
	provider.podsMu.RLock()
	defer provider.podsMu.RUnlock()
	_, ok := provider.pods[uid]
	return ok
}

func TestDeletePodKeepsPodTrackedUntilRemoteDeletionSucceeds(t *testing.T) {
	var attempts atomic.Int32
	serverHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodDelete, r.Method)
		require.Equal(t, "/delete", r.URL.Path)

		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	provider, pod := newDeleteTestProvider(t, serverHandler)

	err := provider.DeletePod(context.Background(), pod.DeepCopy())
	require.Error(t, err)

	stillTracked := podIsTracked(provider, string(pod.UID))
	assert.True(t, stillTracked, "pod must remain tracked when remote deletion fails")

	err = provider.DeletePod(context.Background(), pod.DeepCopy())
	require.NoError(t, err)

	stillTracked = podIsTracked(provider, string(pod.UID))
	assert.False(t, stillTracked, "pod must be removed after remote deletion succeeds")
	assert.Equal(t, int32(2), attempts.Load())
}

func TestDeletePodDoesNotDropPodWhenRemoteDeletionFails(t *testing.T) {
	provider, pod := newDeleteTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))

	err := provider.DeletePod(context.Background(), pod.DeepCopy())
	require.Error(t, err)

	stillTracked := podIsTracked(provider, string(pod.UID))
	assert.True(t, stillTracked)
}
