package virtualkubelet

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func wstunnelPod(name, ip string, created time.Time, terminating bool) *v1.Pod {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         testNamespaceDefault,
			Labels:            map[string]string{"app.kubernetes.io/component": "tunnel"},
			CreationTimestamp: metav1.NewTime(created),
		},
		Status: v1.PodStatus{Phase: v1.PodRunning, PodIP: ip},
	}
	if terminating {
		deleted := metav1.NewTime(created.Add(time.Minute))
		pod.DeletionTimestamp = &deleted
	}
	return pod
}

// The pod of a previous session, still terminating, used to be the one picked:
// its address stops answering seconds later, and whoever was handed it (the
// JupyterHub that has to reach the spawned server, for instance) never learns
// the new one.
func TestCurrentDeploymentPodSkipsTerminatingPods(t *testing.T) {
	now := time.Now()
	p := &Provider{clientSet: fake.NewSimpleClientset(
		wstunnelPod("tunnel-old", "10.42.17.179", now.Add(-time.Minute), true),
		wstunnelPod("tunnel-new", "10.42.15.32", now, false),
	)}

	pod, err := p.currentDeploymentPod(context.Background(), "tunnel", testNamespaceDefault)
	require.NoError(t, err)
	assert.Equal(t, "tunnel-new", pod.Name)
	assert.Equal(t, "10.42.15.32", pod.Status.PodIP)
}

func TestCurrentDeploymentPodIgnoresFinishedPodsAndPicksTheNewest(t *testing.T) {
	now := time.Now()
	finished := wstunnelPod("tunnel-failed", "10.42.17.180", now, false)
	finished.Status.Phase = v1.PodFailed

	p := &Provider{clientSet: fake.NewSimpleClientset(
		finished,
		wstunnelPod("tunnel-older", "10.42.15.10", now.Add(-time.Hour), false),
		wstunnelPod("tunnel-newer", "10.42.15.32", now.Add(-time.Minute), false),
	)}

	pod, err := p.currentDeploymentPod(context.Background(), "tunnel", testNamespaceDefault)
	require.NoError(t, err)
	assert.Equal(t, "tunnel-newer", pod.Name)
}

func TestCurrentDeploymentPodWithoutUsablePod(t *testing.T) {
	now := time.Now()
	p := &Provider{clientSet: fake.NewSimpleClientset(
		wstunnelPod("tunnel-old", "10.42.17.179", now, true),
	)}

	_, err := p.currentDeploymentPod(context.Background(), "tunnel", testNamespaceDefault)
	assert.Error(t, err)
}

func TestWaitForWstunnelPodIPSkipsTerminatingPods(t *testing.T) {
	now := time.Now()
	p := &Provider{clientSet: fake.NewSimpleClientset(
		wstunnelPod("tunnel-old", "10.42.17.179", now.Add(-time.Minute), true),
		wstunnelPod("tunnel-new", "10.42.15.32", now, false),
	)}
	// The pod handed over by the deployment may be the one going away.
	dummyPod := wstunnelPod("tunnel-old", "10.42.17.179", now.Add(-time.Minute), true)

	ip, err := p.waitForWstunnelPodIP(context.Background(), dummyPod, 2*time.Second,
		wstunnelResourceIdentity{Name: "tunnel", Namespace: testNamespaceDefault})
	require.NoError(t, err)
	assert.Equal(t, "10.42.15.32", ip)
}

func TestWstunnelServiceIP(t *testing.T) {
	service := func(clusterIP string) *v1.Service {
		return &v1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "tunnel", Namespace: testNamespaceDefault},
			Spec:       v1.ServiceSpec{ClusterIP: clusterIP},
		}
	}
	identity := wstunnelResourceIdentity{Name: "tunnel", Namespace: testNamespaceDefault}

	tests := []struct {
		name string
		p    *Provider
		want string
	}{
		{"cluster IP", &Provider{clientSet: fake.NewSimpleClientset(service("10.43.204.202"))}, "10.43.204.202"},
		// Both fall back to the gateway pod IP
		{"headless service", &Provider{clientSet: fake.NewSimpleClientset(service(v1.ClusterIPNone))}, ""},
		{"no service", &Provider{clientSet: fake.NewSimpleClientset()}, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.p.wstunnelServiceIP(context.Background(), identity))
		})
	}
}
