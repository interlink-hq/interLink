package interlink

import (
	"io/ioutil"
	"net/http"
	"os"

	"github.com/containerd/containerd/log"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

func SetKubeCFGHandler(w http.ResponseWriter, r *http.Request) {
	log.G(Ctx).Info("InterLink: received SetKubeCFG call")
	path := "/tmp/.kube/"
	statusCode := http.StatusOK

	bodyBytes, err := ioutil.ReadAll(r.Body)
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		w.Write([]byte(err.Error()))
		log.G(Ctx).Fatal(err)
	}

	log.G(Ctx).Debug("- Creating folder to save KubeConfig")
	err = os.MkdirAll(path, os.ModePerm)
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		w.Write([]byte(err.Error()))
		log.G(Ctx).Fatal(err)
	} else {
		log.G(Ctx).Debug("-- Created folder")
	}
	log.G(Ctx).Debug("- Creating the actual KubeConfig file")
	config, err := os.Create(path + "config")
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		w.Write([]byte(err.Error()))
		log.G(Ctx).Fatal(err)
	} else {
		log.G(Ctx).Debug("-- Created file")
	}
	log.G(Ctx).Debug("- Writing configuration to file")
	_, err = config.Write([]byte(bodyBytes))
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		w.Write([]byte(err.Error()))
		log.G(Ctx).Fatal(err)
	} else {
		log.G(Ctx).Info("-- Written configuration")
	}
	defer config.Close()
	log.G(Ctx).Debug("- Setting KUBECONFIG env")
	err = os.Setenv("KUBECONFIG", path+"config")
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		w.Write([]byte(err.Error()))
		log.G(Ctx).Fatal(err)
	} else {
		log.G(Ctx).Info("-- Set KUBECONFIG to " + path + "config")
	}

	kubeconfig, err := clientcmd.BuildConfigFromFlags("", os.Getenv("KUBECONFIG"))
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		w.Write([]byte(err.Error()))
		log.G(Ctx).Fatal("Unable to create a valid config")
	}
	Clientset, err = kubernetes.NewForConfig(kubeconfig)
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		w.Write([]byte(err.Error()))
		log.G(Ctx).Fatal("Unable to set up a clientset")
	}

	w.WriteHeader(statusCode)
	w.Write([]byte("OK"))
}
