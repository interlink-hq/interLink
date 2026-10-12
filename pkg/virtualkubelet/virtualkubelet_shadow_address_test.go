package virtualkubelet

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// The address handed to the virtual pod has to be the shadow service one: the
// shadow pod IP stops answering as soon as that pod is replaced (a new session,
// a rescheduling), and it is never updated on the virtual pod afterwards, so
// whoever connects to it (the JupyterHub that has to reach the spawned server,
// for instance) never learns the new one.
func TestShadowServiceIP(t *testing.T) {
	service := func(clusterIP string, ports ...v1.ServicePort) *v1.Service {
		return &v1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "tunnel", Namespace: testNamespaceDefault},
			Spec:       v1.ServiceSpec{ClusterIP: clusterIP, Ports: ports},
		}
	}
	tcpPort := v1.ServicePort{Name: "http", Port: 8888, Protocol: v1.ProtocolTCP}
	identity := shadowResourceIdentity{Name: "tunnel", Namespace: testNamespaceDefault}

	tests := []struct {
		name string
		p    *Provider
		want string
	}{
		{
			"cluster IP",
			&Provider{clientSet: fake.NewSimpleClientset(service("10.43.204.202", tcpPort))},
			"10.43.204.202",
		},
		// Every case below falls back to the shadow pod IP.
		{
			"headless service",
			&Provider{clientSet: fake.NewSimpleClientset(service(v1.ClusterIPNone, tcpPort))},
			"",
		},
		{
			"service without ports",
			&Provider{clientSet: fake.NewSimpleClientset(service("10.43.204.202"))},
			"",
		},
		{
			"no service",
			&Provider{clientSet: fake.NewSimpleClientset()},
			"",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.p.shadowServiceIP(context.Background(), identity))
		})
	}
}
