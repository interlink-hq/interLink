package virtualkubelet

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

const (
	testEvictedShadowIP     = "10.42.15.6"
	testReplacementShadowIP = "10.42.17.106"
)

// servingShadowPod is a running and ready pod of the given shadow.
func servingShadowPod(identity shadowResourceIdentity, name, ip string, created time.Time) *v1.Pod {
	pod := shadowTestPod(name, identity.Name, ip, created)
	pod.Namespace = identity.Namespace
	pod.Status.Phase = v1.PodRunning
	pod.Status.Conditions = []v1.PodCondition{{Type: v1.PodReady, Status: v1.ConditionTrue}}
	return pod
}

func markTerminating(pod *v1.Pod) *v1.Pod {
	deleted := metav1.Now()
	pod.DeletionTimestamp = &deleted
	return pod
}

func markNotReady(pod *v1.Pod) *v1.Pod {
	pod.Status.Conditions = []v1.PodCondition{{Type: v1.PodReady, Status: v1.ConditionFalse}}
	return pod
}

// replacedShadow is an offloaded pod created at now, still reporting the IP of the
// shadow pod it was created with, together with that shadow pod, now being evicted,
// and the pod the shadow Deployment started in its place.
func replacedShadow(t *testing.T, now time.Time) (pod, evicted, replacement *v1.Pod) {
	t.Helper()
	pod = podWithPort("web-5d8f7c9b4-x2x4z", testNamespaceDefault, "uid-web")
	pod.CreationTimestamp = metav1.NewTime(now)
	status, err := PodPhase(nil, "Running", testEvictedShadowIP)
	require.NoError(t, err)
	pod.Status = status

	identity, err := computeShadowResourceIdentity(pod)
	require.NoError(t, err)
	evicted = markTerminating(servingShadowPod(identity, identity.Name+"-6d5c666bc8-aaaaa", testEvictedShadowIP, now))
	replacement = servingShadowPod(identity, identity.Name+"-6d5c666bc8-zzzzz", testReplacementShadowIP, now.Add(time.Minute))
	return pod, evicted, replacement
}

func shadowAddressProvider(pod *v1.Pod) *Provider {
	p := tunnelProvider(fake.NewSimpleClientset())
	p.pods = map[string]*v1.Pod{string(pod.UID): pod}
	return p
}

// A Deployment offloaded with a Service in front: the Service's EndpointSlice takes
// the virtual pod's status.podIP, so that has to be an address that routes, the
// shadow pod's (a ClusterIP does not: kube-proxy does not route to Service IPs), and
// it has to follow the shadow pod when the shadow Deployment replaces it. It used to
// be written once, at creation, leaving the Service pointing at a pod that is gone.
func TestRefreshShadowPodIPFollowsAReplacedShadowPod(t *testing.T) {
	pod, evicted, replacement := replacedShadow(t, time.Now())
	p := shadowAddressProvider(pod)

	p.refreshShadowPodIP(t.Context(), pod.DeepCopy(), indexShadowPods([]v1.Pod{*evicted, *replacement}))

	status := p.pods[string(pod.UID)].Status
	assert.Equal(t, testReplacementShadowIP, status.PodIP)
	assert.Equal(t, []v1.PodIP{{IP: testReplacementShadowIP}}, status.PodIPs)
	assert.Equal(t, testReplacementShadowIP, status.HostIP)
	assert.Equal(t, []v1.HostIP{{IP: testReplacementShadowIP}}, status.HostIPs,
		"the API server rejects a status whose hostIPs[0] differs from hostIP")
}

// The status loop is what hands the pod's status to the API server, the status the
// Service's EndpointSlice is built from: the first poll after the shadow Deployment
// replaced its pod already reports the replacement's IP.
func TestStatusLoopReportsTheReplacementShadowPodIP(t *testing.T) {
	plugin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := w.Write([]byte("[]")); err != nil {
			panic(err)
		}
	}))
	t.Cleanup(plugin.Close)
	pluginURL, err := url.Parse(plugin.URL)
	require.NoError(t, err)
	host, port, err := net.SplitHostPort(pluginURL.Host)
	require.NoError(t, err)

	pod, evicted, replacement := replacedShadow(t, time.Now())
	p := shadowAddressProvider(pod)
	p.clientSet = fake.NewSimpleClientset(evicted, replacement)
	p.config.InterlinkURL = "http://" + host
	p.config.InterlinkPort = port
	notified := make(chan *v1.Pod, 1)
	p.notifier = func(pod *v1.Pod) {
		select {
		case notified <- pod:
		default:
		}
	}

	statusLoopInterval = 10 * time.Millisecond
	t.Cleanup(func() { statusLoopInterval = 5 * time.Second })
	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan struct{})
	go func() {
		p.statusLoop(ctx)
		close(stopped)
	}()
	t.Cleanup(func() {
		cancel()
		<-stopped
	})

	select {
	case got := <-notified:
		assert.Equal(t, testReplacementShadowIP, got.Status.PodIP)
	case <-time.After(5 * time.Second):
		t.Fatal("the status loop never reported the pod")
	}
}

func TestRefreshShadowPodIPLeavesOtherAddressesAlone(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(p *Provider, pod *v1.Pod)
		unlisted bool
	}{
		{
			// The mesh renders a shadow for every pod, and the VPN still overrides its IP.
			name: "address from the VPN",
			setup: func(p *Provider, pod *v1.Pod) {
				p.config.Network.FullMesh = true
				pod.Annotations = map[string]string{"interlink.eu/pod-vpn": valueTrue}
			},
		},
		{
			name: "address from the annotation",
			setup: func(_ *Provider, pod *v1.Pod) {
				pod.Annotations = map[string]string{"interlink.eu/pod-ip": testEvictedShadowIP}
			},
		},
		{
			name:  "pod without a tunnel shadow",
			setup: func(_ *Provider, pod *v1.Pod) { pod.Spec.Containers[0].Ports = nil },
		},
		{
			name:  "pod waiting for its remote deletion",
			setup: func(p *Provider, pod *v1.Pod) { p.markDeletePending(pod) },
		},
		{
			name:     "pods could not be listed",
			unlisted: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pod, evicted, replacement := replacedShadow(t, time.Now())
			p := shadowAddressProvider(pod)
			if tc.setup != nil {
				tc.setup(p, pod)
			}
			index := indexShadowPods([]v1.Pod{*evicted, *replacement})
			if tc.unlisted {
				index = indexShadowPods(nil)
			}

			p.refreshShadowPodIP(t.Context(), pod.DeepCopy(), index)

			assert.Equal(t, testEvictedShadowIP, p.pods[string(pod.UID)].Status.PodIP)
		})
	}
}

func TestCurrentShadowPodIP(t *testing.T) {
	now := time.Now()
	identity := shadowResourceIdentity{Name: "web-default", Namespace: testNamespaceDefault + shadowNamespaceSuffix}
	shadowPod := func(suffix, ip string, created time.Time) *v1.Pod {
		return servingShadowPod(identity, identity.Name+"-6d5c666bc8-"+suffix, ip, created)
	}
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{CreationTimestamp: metav1.NewTime(now)},
		Status:     v1.PodStatus{PodIP: "10.42.0.1"},
	}

	tests := []struct {
		name       string
		shadowPods []*v1.Pod
		want       string
	}{
		{
			name: "keeps the address while its pod serves, even next to a newer one",
			shadowPods: []*v1.Pod{
				shadowPod("aaaaa", "10.42.0.1", now),
				shadowPod("bbbbb", "10.42.0.2", now.Add(time.Minute)),
			},
			want: "10.42.0.1",
		},
		{
			name: "moves to the newest serving pod once its pod is gone",
			shadowPods: []*v1.Pod{
				shadowPod("bbbbb", "10.42.0.2", now.Add(time.Minute)),
				shadowPod("ccccc", "10.42.0.3", now.Add(2*time.Minute)),
			},
			want: "10.42.0.3",
		},
		{
			name: "moves away from its pod once it terminates",
			shadowPods: []*v1.Pod{
				markTerminating(shadowPod("aaaaa", "10.42.0.1", now)),
				shadowPod("bbbbb", "10.42.0.2", now.Add(time.Minute)),
			},
			want: "10.42.0.2",
		},
		{
			name:       "waits for the replacement to be ready",
			shadowPods: []*v1.Pod{markNotReady(shadowPod("bbbbb", "10.42.0.2", now.Add(time.Minute)))},
		},
		{
			name:       "never moves to a terminating pod",
			shadowPods: []*v1.Pod{markTerminating(shadowPod("bbbbb", "10.42.0.2", now.Add(time.Minute)))},
		},
		{
			// A StatefulSet replica recreated under its name gets a shadow with the same
			// name as the previous one, whose pods may still be around and serving.
			name:       "never moves to a pod of a previous shadow",
			shadowPods: []*v1.Pod{shadowPod("bbbbb", "10.42.0.2", now.Add(-time.Minute))},
		},
		{
			name: "no shadow pod",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, currentShadowPodIP(pod, tc.shadowPods))
		})
	}
}

func TestIndexShadowPods(t *testing.T) {
	now := time.Now()
	web := shadowResourceIdentity{Name: "web-default", Namespace: testNamespaceDefault + shadowNamespaceSuffix}
	sameNameElsewhere := shadowResourceIdentity{Name: web.Name, Namespace: "other" + shadowNamespaceSuffix}
	first := servingShadowPod(web, "web-default-6d5c666bc8-aaaaa", "10.42.0.1", now)
	second := servingShadowPod(web, "web-default-6d5c666bc8-bbbbb", "10.42.0.2", now)
	elsewhere := servingShadowPod(sameNameElsewhere, "web-default-6d5c666bc8-ccccc", "10.42.0.3", now)
	unrelated := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "coredns", Namespace: "kube-system"}}

	index := indexShadowPods([]v1.Pod{*first, *unrelated, *second, *elsewhere})

	require.Len(t, index, 2)
	require.Len(t, index[web], 2)
	assert.Equal(t, first.Name, index[web][0].Name)
	assert.Equal(t, second.Name, index[web][1].Name)
	require.Len(t, index[sameNameElsewhere], 1)
	assert.Equal(t, elsewhere.Name, index[sameNameElsewhere][0].Name)
}
