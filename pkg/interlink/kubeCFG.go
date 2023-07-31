package interlink

import (
	"encoding/json"
	"io/ioutil"
	"net/http"
	"os"

	"github.com/containerd/containerd/log"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	commonIL "github.com/intertwin-eu/interlink/pkg/common"
)

func SetKubeCFGHandler(w http.ResponseWriter, r *http.Request) {
	log.G(Ctx).Info("InterLink: received SetKubeCFG call")
	path := "/tmp/.kube/"
	retCode := "200"
	bodyBytes, err := ioutil.ReadAll(r.Body)
	if err != nil {
		log.G(Ctx).Error(err)
	}

	var req commonIL.GenericRequestType
	json.Unmarshal(bodyBytes, &req)

	log.G(Ctx).Debug("- Creating folder to save KubeConfig")
	err = os.MkdirAll(path, os.ModePerm)
	if err != nil {
		log.G(Ctx).Error(err)
		retCode = "500"
		w.Write([]byte(retCode))
		return
	} else {
		log.G(Ctx).Debug("-- Created folder")
	}
	log.G(Ctx).Debug("- Creating the actual KubeConfig file")
	config, err := os.Create(path + "config")
	if err != nil {
		log.G(Ctx).Error(err)
		retCode = "500"
		w.Write([]byte(retCode))
		return
	} else {
		log.G(Ctx).Debug("-- Created file")
	}
	log.G(Ctx).Debug("- Writing configuration to file")
	_, err = config.Write([]byte(req.Body))
	if err != nil {
		log.G(Ctx).Error(err)
		retCode = "500"
		w.Write([]byte(retCode))
		return
	} else {
		log.G(Ctx).Info("-- Written configuration")
	}
	defer config.Close()
	log.G(Ctx).Debug("- Setting KUBECONFIG env")
	err = os.Setenv("KUBECONFIG", path+"config")
	if err != nil {
		log.G(Ctx).Error(err)
		retCode = "500"
		w.Write([]byte(retCode))
		return
	} else {
		log.G(Ctx).Info("-- Set KUBECONFIG to " + path + "config")
	}

	kubeconfig, err := clientcmd.BuildConfigFromFlags("", os.Getenv("KUBECONFIG"))
	if err != nil {
		log.G(Ctx).Error("Unable to create a valid config")
		return
	}
	Clientset, err = kubernetes.NewForConfig(kubeconfig)
	if err != nil {
		log.G(Ctx).Fatalln("Unable to set up a clientset")
	}

	w.Write([]byte(retCode))
}
