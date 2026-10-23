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
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

const (
	testEvictedShadowIP     = "10.42.15.6"
	testReplacementShadowIP = "10.42.17.106"
)

// shadowPodOf is a running and ready pod of the given pod's shadow, labelled with that
// pod's UID like every shadow pod.
func shadowPodOf(t *testing.T, owner *v1.Pod, suffix, ip string, created time.Time) *v1.Pod {
	t.Helper()
	identity, err := computeShadowResourceIdentity(owner)
	require.NoError(t, err)
	pod := shadowTestPod(identity.Name+"-6d5c666bc8-"+suffix, identity.Name, ip, created)
	pod.Namespace = identity.Namespace
	pod.Labels[shadowOfLabel] = string(owner.UID)
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

// replacedShadow is an offloaded pod still reporting the IP of the shadow pod it was
// created with, together with that shadow pod, now being evicted, and the pod the
// shadow Deployment started in its place.
func replacedShadow(t *testing.T, now time.Time) (pod, evicted, replacement *v1.Pod) {
	t.Helper()
	pod = podWithPort("web-5d8f7c9b4-x2x4z", testNamespaceDefault, "uid-web")
	status, err := PodPhase(nil, "Running", testEvictedShadowIP)
	require.NoError(t, err)
	pod.Status = status

	evicted = markTerminating(shadowPodOf(t, pod, "aaaaa", testEvictedShadowIP, now))
	replacement = shadowPodOf(t, pod, "zzzzz", testReplacementShadowIP, now.Add(time.Minute))
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

// A pod recreated under its name, like a StatefulSet replica, gets a shadow with the
// same name as the previous one, whose ReplicaSet can still start a pod, after the new
// pod was created, until the garbage collector removes it. The new shadow's pod only
// needs an IP for the pod to be created with it, not to be ready: if the previous
// shadow's pod is ready first, it must not take the IP over.
func TestRefreshShadowPodIPIgnoresThePreviousShadow(t *testing.T) {
	now := time.Now()
	pod := podWithPort("nb-0", testNamespaceDefault, "uid-nb-0")
	pod.CreationTimestamp = metav1.NewTime(now)
	previous := podWithPort(pod.Name, pod.Namespace, "uid-nb-0-previous")
	stale := shadowPodOf(t, previous, "aaaaa", "10.42.9.12", now.Add(time.Second))
	current := markNotReady(shadowPodOf(t, pod, "zzzzz", "10.42.21.3", now.Add(2*time.Second)))
	status, err := PodPhase(nil, "Pending", current.Status.PodIP)
	require.NoError(t, err)
	pod.Status = status
	p := shadowAddressProvider(pod)

	p.refreshShadowPodIP(t.Context(), pod.DeepCopy(), indexShadowPods([]v1.Pod{*stale, *current}))

	assert.Equal(t, current.Status.PodIP, p.pods[string(pod.UID)].Status.PodIP)
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
		name       string
		setup      func(p *Provider, pod *v1.Pod)
		shadowPods func(evicted, replacement *v1.Pod) []v1.Pod
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
			// Nothing tells its pods apart from those of a previous shadow with the same name.
			name: "shadow rendered before the shadow-of label",
			shadowPods: func(evicted, replacement *v1.Pod) []v1.Pod {
				delete(evicted.Labels, shadowOfLabel)
				delete(replacement.Labels, shadowOfLabel)
				return []v1.Pod{*evicted, *replacement}
			},
		},
		{
			name:       "pods could not be listed",
			shadowPods: func(*v1.Pod, *v1.Pod) []v1.Pod { return nil },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pod, evicted, replacement := replacedShadow(t, time.Now())
			p := shadowAddressProvider(pod)
			if tc.setup != nil {
				tc.setup(p, pod)
			}
			shadowPods := []v1.Pod{*evicted, *replacement}
			if tc.shadowPods != nil {
				shadowPods = tc.shadowPods(evicted, replacement)
			}

			p.refreshShadowPodIP(t.Context(), pod.DeepCopy(), indexShadowPods(shadowPods))

			assert.Equal(t, testEvictedShadowIP, p.pods[string(pod.UID)].Status.PodIP)
		})
	}
}

func TestCurrentShadowPodIP(t *testing.T) {
	now := time.Now()
	owner := podWithPort("web", testNamespaceDefault, "uid-web")
	shadowPod := func(suffix, ip string, created time.Time) *v1.Pod {
		return shadowPodOf(t, owner, suffix, ip, created)
	}
	const reported = "10.42.0.1"

	tests := []struct {
		name       string
		shadowPods []*v1.Pod
		want       string
	}{
		{
			name: "keeps the IP while its pod serves, even next to a newer one",
			shadowPods: []*v1.Pod{
				shadowPod("aaaaa", reported, now),
				shadowPod("bbbbb", "10.42.0.2", now.Add(time.Minute)),
			},
			want: reported,
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
				markTerminating(shadowPod("aaaaa", reported, now)),
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
			name: "no shadow pod",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, currentShadowPodIP(reported, tc.shadowPods))
		})
	}
}

func TestIndexShadowPods(t *testing.T) {
	now := time.Now()
	web := podWithPort("web", testNamespaceDefault, "uid-web")
	other := podWithPort("web", "other", "uid-other")
	first := shadowPodOf(t, web, "aaaaa", "10.42.0.1", now)
	second := shadowPodOf(t, web, "bbbbb", "10.42.0.2", now)
	ofOther := shadowPodOf(t, other, "ccccc", "10.42.0.3", now)
	// A pod of a shadow rendered before shadowOfLabel existed.
	unlabeled := shadowPodOf(t, web, "ddddd", "10.42.0.4", now)
	delete(unlabeled.Labels, shadowOfLabel)
	unrelated := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "coredns", Namespace: "kube-system"}}

	index := indexShadowPods([]v1.Pod{*first, *unrelated, *second, *unlabeled, *ofOther})

	require.Len(t, index, 2)
	require.Len(t, index[string(web.UID)], 2)
	assert.Equal(t, first.Name, index[string(web.UID)][0].Name)
	assert.Equal(t, second.Name, index[string(web.UID)][1].Name)
	require.Len(t, index[string(other.UID)], 1)
	assert.Equal(t, ofOther.Name, index[string(other.UID)][0].Name)
}

// The label is what ties the pods of a shadow to their pod, so createShadowPod has to
// put it on them when it creates the shadow, and replace it when the pod, recreated
// under its name, takes over the Deployment its previous shadow left behind.
func TestCreateShadowPodLabelsItsPodsWithThePodUID(t *testing.T) {
	pod := podWithPort("nb-0", testNamespaceDefault, "uid-nb-0")
	identity, err := computeShadowResourceIdentity(pod)
	require.NoError(t, err)

	tests := []struct {
		name     string
		existing []runtime.Object
	}{
		{name: "new shadow"},
		{
			name: "Deployment left by the previous shadow",
			existing: []runtime.Object{&appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Name: identity.Name, Namespace: identity.Namespace},
				Spec: appsv1.DeploymentSpec{Template: v1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{shadowComponentLabel: identity.Name, shadowOfLabel: "uid-nb-0-previous"},
				}}},
			}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// The pod waitForDeploymentPod returns once the Deployment is applied.
			running := &v1.Pod{ObjectMeta: metav1.ObjectMeta{
				Name:      identity.Name + "-0",
				Namespace: identity.Namespace,
				Labels:    map[string]string{shadowComponentLabel: identity.Name},
			}}
			client := fake.NewSimpleClientset(append([]runtime.Object{running}, tc.existing...)...)
			p := tunnelProvider(client)

			_, _, err := p.createShadowPod(t.Context(), pod)
			require.NoError(t, err)

			deployment, err := client.AppsV1().Deployments(identity.Namespace).Get(t.Context(), identity.Name, metav1.GetOptions{})
			require.NoError(t, err)
			assert.Equal(t, string(pod.UID), deployment.Spec.Template.Labels[shadowOfLabel])
		})
	}
}
