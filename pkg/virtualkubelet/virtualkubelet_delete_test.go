package virtualkubelet

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const deleteTestContainer = "main"

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

func deleteIsPending(provider *Provider, uid string) bool {
	provider.pendingDeletesMu.Lock()
	defer provider.pendingDeletesMu.Unlock()
	_, ok := provider.pendingDeletes[uid]
	return ok
}

// deleteHandler records every /delete call and replies with the given status codes
// in order, repeating the last one once they are exhausted.
func deleteHandler(attempts *atomic.Int32, method, path *atomic.Value, statuses ...int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		method.Store(r.Method)
		path.Store(r.URL.Path)

		idx := int(attempts.Add(1)) - 1
		if idx >= len(statuses) {
			idx = len(statuses) - 1
		}
		w.WriteHeader(statuses[idx])
	}
}

func TestDeletePodKeepsPodTrackedUntilRemoteDeletionSucceeds(t *testing.T) {
	var attempts atomic.Int32
	var method, path atomic.Value

	provider, pod := newDeleteTestProvider(t, deleteHandler(&attempts, &method, &path,
		http.StatusServiceUnavailable, http.StatusOK))

	err := provider.DeletePod(context.Background(), pod.DeepCopy())
	require.Error(t, err)

	assert.True(t, podIsTracked(provider, string(pod.UID)), "pod must remain tracked when remote deletion fails")
	assert.True(t, deleteIsPending(provider, string(pod.UID)), "pod must stay on the retry sweep")

	err = provider.DeletePod(context.Background(), pod.DeepCopy())
	require.NoError(t, err)

	assert.False(t, podIsTracked(provider, string(pod.UID)), "pod must be removed after remote deletion succeeds")
	assert.False(t, deleteIsPending(provider, string(pod.UID)), "pod must leave the retry sweep once confirmed")
	assert.Equal(t, int32(2), attempts.Load())
	assert.Equal(t, http.MethodDelete, method.Load())
	assert.Equal(t, "/delete", path.Load())
}

func TestDeletePodDoesNotDropPodWhenRemoteDeletionFails(t *testing.T) {
	var attempts atomic.Int32
	var method, path atomic.Value

	provider, pod := newDeleteTestProvider(t, deleteHandler(&attempts, &method, &path,
		http.StatusInternalServerError))

	err := provider.DeletePod(context.Background(), pod.DeepCopy())
	require.Error(t, err)

	assert.True(t, podIsTracked(provider, string(pod.UID)))
	assert.True(t, deleteIsPending(provider, string(pod.UID)))
}

// The pod controller stops calling DeletePod once it exhausts its retries, or as
// soon as the pod is no longer running. The sweep must still finish the job.
func TestReconcilePendingDeletesRetriesAfterControllerStopsCalling(t *testing.T) {
	var attempts atomic.Int32
	var method, path atomic.Value

	provider, pod := newDeleteTestProvider(t, deleteHandler(&attempts, &method, &path,
		http.StatusServiceUnavailable, http.StatusServiceUnavailable, http.StatusOK))

	// Single failed delete from the controller, which then gives up.
	require.Error(t, provider.DeletePod(context.Background(), pod.DeepCopy()))
	require.Equal(t, int32(1), attempts.Load())

	// Second attempt, driven only by the sweep, still fails.
	provider.reconcilePendingDeletes(context.Background(), time.Now().Add(time.Hour))
	assert.Equal(t, int32(2), attempts.Load())
	assert.True(t, podIsTracked(provider, string(pod.UID)))

	// Third attempt succeeds and the pod is finally released.
	provider.reconcilePendingDeletes(context.Background(), time.Now().Add(2*time.Hour))
	assert.Equal(t, int32(3), attempts.Load())
	assert.False(t, podIsTracked(provider, string(pod.UID)), "sweep must drop the pod once the plugin confirms")
	assert.False(t, deleteIsPending(provider, string(pod.UID)))

	// Nothing left to retry.
	provider.reconcilePendingDeletes(context.Background(), time.Now().Add(3*time.Hour))
	assert.Equal(t, int32(3), attempts.Load())
}

func TestReconcilePendingDeletesHonoursBackoff(t *testing.T) {
	var attempts atomic.Int32
	var method, path atomic.Value

	provider, pod := newDeleteTestProvider(t, deleteHandler(&attempts, &method, &path,
		http.StatusServiceUnavailable))

	require.Error(t, provider.DeletePod(context.Background(), pod.DeepCopy()))
	require.Equal(t, int32(1), attempts.Load())

	// Still inside the backoff window: no request must be issued.
	provider.reconcilePendingDeletes(context.Background(), time.Now())
	assert.Equal(t, int32(1), attempts.Load())

	// Past the backoff window: retried.
	provider.reconcilePendingDeletes(context.Background(), time.Now().Add(deleteRetryBaseBackoff+time.Second))
	assert.Equal(t, int32(2), attempts.Load())
}

func TestReconcilePendingDeletesReportsTerminatedStatus(t *testing.T) {
	var attempts atomic.Int32
	var method, path atomic.Value

	provider, pod := newDeleteTestProvider(t, deleteHandler(&attempts, &method, &path,
		http.StatusServiceUnavailable, http.StatusOK))

	provider.podsMu.Lock()
	provider.pods[string(pod.UID)].Status.ContainerStatuses = []v1.ContainerStatus{
		{Name: deleteTestContainer, Ready: true, State: v1.ContainerState{Running: &v1.ContainerStateRunning{}}},
	}
	provider.podsMu.Unlock()

	var notified *v1.Pod
	provider.notifier = func(p *v1.Pod) { notified = p }

	require.Error(t, provider.DeletePod(context.Background(), pod.DeepCopy()))
	require.Nil(t, notified, "no terminated status must be reported while the deletion is unconfirmed")

	provider.reconcilePendingDeletes(context.Background(), time.Now().Add(time.Hour))

	require.NotNil(t, notified, "the sweep must report the terminated status once confirmed")
	assert.Equal(t, "VKProviderPodDeleted", notified.Status.Reason)
	require.Len(t, notified.Status.ContainerStatuses, 1)
	assert.False(t, notified.Status.ContainerStatuses[0].Ready)
	require.NotNil(t, notified.Status.ContainerStatuses[0].State.Terminated)
	assert.Equal(t, "VKProviderPodContainerDeleted", notified.Status.ContainerStatuses[0].State.Terminated.Reason)
}

func TestDeleteRetryBackoffIsCapped(t *testing.T) {
	assert.Equal(t, deleteRetryBaseBackoff, deleteRetryBackoff(1))
	assert.Equal(t, 2*deleteRetryBaseBackoff, deleteRetryBackoff(2))
	assert.Equal(t, 4*deleteRetryBaseBackoff, deleteRetryBackoff(3))
	assert.Equal(t, deleteRetryMaxBackoff, deleteRetryBackoff(100))
}
