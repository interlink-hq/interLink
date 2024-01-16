package slurm

import (
	"encoding/json"
	"io"
	"net/http"
	"os"

	"github.com/containerd/containerd/log"
	commonIL "github.com/intertwin-eu/interlink/pkg/common"
	v1 "k8s.io/api/core/v1"
)

func StopHandler(w http.ResponseWriter, r *http.Request) {
	log.G(Ctx).Info("Slurm Sidecar: received Stop call")
	statusCode := http.StatusOK

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		w.Write([]byte("Some errors occurred while deleting container. Check Slurm Sidecar's logs"))
		log.G(Ctx).Error(err)
		return
	}

	var pod *v1.Pod
	err = json.Unmarshal(bodyBytes, &pod)
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		w.Write([]byte("Some errors occurred while deleting container. Check Slurm Sidecar's logs"))
		log.G(Ctx).Error(err)
		return
	}

	filesPath := commonIL.InterLinkConfigInst.DataRootFolder + pod.Namespace + "-" + string(pod.UID)

	err = delete_container(string(pod.UID), filesPath+"/"+pod.Namespace)
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		w.Write([]byte("Error deleting containers. Check Slurm Sidecar's logs"))
		log.G(Ctx).Error(err)
		return
	}
	if os.Getenv("SHARED_FS") != "true" {
		err = os.RemoveAll(filesPath)
	}

	w.WriteHeader(statusCode)
	if statusCode != http.StatusOK {
		w.Write([]byte("Some errors occurred deleting containers. Check Slurm Sidecar's logs"))
	} else {

		w.Write([]byte("All containers for submitted Pods have been deleted"))
	}
}
