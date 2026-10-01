package virtualkubelet

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func shadowTestPod(name, deploymentName, ip string, created time.Time) *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         testNamespace,
			Labels:            map[string]string{"app.kubernetes.io/component": deploymentName},
			CreationTimestamp: metav1.NewTime(created),
		},
		Status: v1.PodStatus{PodIP: ip},
	}
}

// A StatefulSet replica (a Kubeflow notebook) is recreated under the same name, so its
// shadow Deployment has the same name, labels and pod-template hash as the one just
// deleted. The garbage collector removes the old Deployment's pods asynchronously; if
// one of them is picked, its IP is reported as the new pod's IP and goes stale as soon
// as it terminates.
func TestWaitForDeploymentPodIgnoresPodsOfAPreviousDeployment(t *testing.T) {
	const name = "shadow-nb-0-ns"
	now := time.Now()

	deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
		Name: name, Namespace: testNamespace, CreationTimestamp: metav1.NewTime(now),
	}}
	previous := shadowTestPod(name+"-6d5c666bc8-aaaaa", name, "10.0.0.146", now.Add(-10*time.Minute))
	current := shadowTestPod(name+"-6d5c666bc8-zzzzz", name, "10.0.0.141", now)

	p := &Provider{clientSet: fake.NewSimpleClientset(deployment, previous, current)}

	got, err := p.waitForDeploymentPod(context.Background(), name, testNamespace)
	require.NoError(t, err)
	assert.Equal(t, current.Name, got.Name)
}

func TestWaitForDeploymentPodIgnoresTerminatingPods(t *testing.T) {
	const name = "shadow-nb-0-ns"
	now := time.Now()

	deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
		Name: name, Namespace: testNamespace, CreationTimestamp: metav1.NewTime(now),
	}}
	terminating := shadowTestPod(name+"-6d5c666bc8-aaaaa", name, "10.0.0.146", now)
	deleted := metav1.NewTime(now)
	terminating.DeletionTimestamp = &deleted
	terminating.Finalizers = []string{"test/keep"}
	current := shadowTestPod(name+"-6d5c666bc8-zzzzz", name, "10.0.0.141", now)

	p := &Provider{clientSet: fake.NewSimpleClientset(deployment, terminating, current)}

	got, err := p.waitForDeploymentPod(context.Background(), name, testNamespace)
	require.NoError(t, err)
	assert.Equal(t, current.Name, got.Name)
}
