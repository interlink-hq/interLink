package tests

import (
	"context"
	"flag"
	"path/filepath"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

func TestCreatePod(t *testing.T) {
	var kubeconfig *string
	if home := homedir.HomeDir(); home != "" {
		kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	}
	flag.Parse()

	// use the current context in kubeconfig
	config, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
	if err != nil {
		panic(err.Error())
	}

	// create the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}

	checks := []struct {
		name        string
		selector    corev1.NodeSelector
		tolerations corev1.Toleration
		volumes     []corev1.Volume
		attachment  []corev1.VolumeMount
		fail        bool
	}{
		{
			name:        "valid-selector-no-volumes",
			selector:    corev1.NodeSelector{},
			tolerations: corev1.Toleration{},
			volumes:     []corev1.Volume{},
			attachment:  []corev1.VolumeMount{},
			fail:        false,
		},
	}

	for _, tt := range checks {
		t.Run(tt.name, func(t *testing.T) {

			podManifest := corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: tt.name,
				},
			}
			pod, err := clientset.CoreV1().Pods("").Create(context.TODO(), &podManifest, metav1.CreateOptions{})
			if err != nil && !tt.fail {
				t.Fatal("did not expect creation to fail:", err)
			} else if err == nil {
				_, err = clientset.CoreV1().Pods("").Get(context.TODO(), pod.ObjectMeta.Name, metav1.GetOptions{})
				if err != nil && !tt.fail {
					t.Fatal("did not expect creation to fail:", err)
				}
			}
		})
	}

}
