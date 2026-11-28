package virtualkubelet

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
)

const (
	vpnTestPodCIDR = "10.30.0.0/24"
	vpnTestTakenIP = "10.30.0.2"
)

func vpnPod(name string) *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   "default",
			UID:         k8stypes.UID(name + "-uid"),
			Annotations: map[string]string{reverseTestPodVPN: "true"},
		},
	}
}

// The address of a VPN pod only lives in the annotation of the pod the provider
// tracks, the API server never sees it. A later pod must still get another address,
// also when nothing but the tracked pod remembers it (e.g. after a status loop run).
func TestSetupVPNPodIPSkipsAddressesOfTrackedPods(t *testing.T) {
	ctx := context.Background()
	tracked := vpnPod("tracked")
	tracked.Annotations["interlink.eu/pod-ip"] = vpnTestTakenIP
	p := &Provider{
		node:      &v1.Node{Spec: v1.NodeSpec{PodCIDR: vpnTestPodCIDR}},
		pods:      map[string]*v1.Pod{string(tracked.UID): tracked},
		config:    Config{PodCIDR: PodCIDR{MinIP: 2, MaxIP: 250}},
		clientSet: fake.NewClientset(vpnPod("tracked")),
	}

	ip, earlyReturn, err := p.setupVPNPodIP(ctx, vpnPod("new"))
	require.NoError(t, err)
	require.False(t, earlyReturn)
	require.NotEmpty(t, ip)
	assert.NotEqual(t, vpnTestTakenIP, ip, "the new VPN pod got the address of a pod this node already runs")
}

func TestSetupVPNPodIPGivesConsecutivePodsDifferentAddresses(t *testing.T) {
	ctx := context.Background()
	p := &Provider{
		node:      &v1.Node{Spec: v1.NodeSpec{PodCIDR: vpnTestPodCIDR}},
		pods:      map[string]*v1.Pod{},
		config:    Config{PodCIDR: PodCIDR{MinIP: 2, MaxIP: 250}},
		clientSet: fake.NewClientset(),
	}

	first := vpnPod("first")
	firstIP, _, err := p.setupVPNPodIP(ctx, first)
	require.NoError(t, err)
	// CreatePod tracks the pod after assigning the address.
	p.pods[string(first.UID)] = first

	secondIP, _, err := p.setupVPNPodIP(ctx, vpnPod("second"))
	require.NoError(t, err)
	assert.NotEqual(t, firstIP, secondIP)
}

func TestSetupVPNPodIPSkipsAddressesAnnotatedInTheCluster(t *testing.T) {
	ctx := context.Background()
	existing := vpnPod("existing")
	existing.Annotations["interlink.eu/pod-ip"] = vpnTestTakenIP
	p := &Provider{
		node:      &v1.Node{Spec: v1.NodeSpec{PodCIDR: vpnTestPodCIDR}},
		pods:      map[string]*v1.Pod{},
		config:    Config{PodCIDR: PodCIDR{MinIP: 2, MaxIP: 250}},
		clientSet: fake.NewClientset(existing),
	}

	ip, _, err := p.setupVPNPodIP(ctx, vpnPod("new"))
	require.NoError(t, err)
	assert.NotEqual(t, vpnTestTakenIP, ip)
}
