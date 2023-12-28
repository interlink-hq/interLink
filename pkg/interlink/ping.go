package interlink

import (
	"net/http"

	"github.com/containerd/containerd/log"
)

func Ping(w http.ResponseWriter, r *http.Request) {
	log.G(Ctx).Info("InterLink: received Ping call")
	// kubeconfig, err := clientcmd.BuildConfigFromFlags("", os.Getenv("KUBECONFIG"))
	// if err != nil {
	// 	log.G(Ctx).Error("Unable to create a valid clientset config")
	// }
	// Clientset, err = kubernetes.NewForConfig(kubeconfig)
	// if err != nil {
	// 	log.G(Ctx).Error("Unable to set up a clientset")
	// }
	w.WriteHeader(http.StatusOK)
}
