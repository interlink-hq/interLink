package docker

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	exec "github.com/alexellis/go-execute/pkg/v1"
	"github.com/containerd/containerd/log"
	commonIL "github.com/intertwin-eu/interlink/pkg/common"
	v1 "k8s.io/api/core/v1"
)

func StatusHandler(w http.ResponseWriter, r *http.Request) {
	log.G(Ctx).Info("Docker Sidecar: received GetStatus call")
	var resp []commonIL.PodStatus
	var req []*v1.Pod
	statusCode := http.StatusOK

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		w.Write([]byte("Some errors occurred while checking container status. Check Docker Sidecar's logs"))
		log.G(Ctx).Error(err)
		return
	}

	err = json.Unmarshal(bodyBytes, &req)
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		w.Write([]byte("Some errors occurred while checking container status. Check Docker Sidecar's logs"))
		log.G(Ctx).Error(err)
		return
	}

	for _, pod := range req {
		for _, container := range pod.Spec.Containers {
			log.G(Ctx).Debug("- Getting status for container " + container.Name)
			cmd := []string{"ps -aqf name=" + container.Name}

			shell := exec.ExecTask{
				Command: "docker",
				Args:    cmd,
				Shell:   true,
			}
			execReturn, err := shell.Execute()
			execReturn.Stdout = strings.ReplaceAll(execReturn.Stdout, "\n", "")

			if err != nil {
				log.G(Ctx).Error(err)
				statusCode = http.StatusInternalServerError
				break
			}

			if execReturn.Stdout == "" {
				log.G(Ctx).Info("-- Container " + container.Name + " is not running")
				resp = append(resp, commonIL.PodStatus{PodName: pod.Name, PodNamespace: pod.Namespace, PodStatus: commonIL.STOP})
			} else {
				log.G(Ctx).Info("-- Container " + container.Name + " is running")
				resp = append(resp, commonIL.PodStatus{PodName: pod.Name, PodNamespace: pod.Namespace, PodStatus: commonIL.RUNNING})
			}
		}
	}

	w.WriteHeader(statusCode)

	if statusCode != http.StatusOK {
		w.Write([]byte("Some errors occurred while checking container status. Check Docker Sidecar's logs"))
	} else {
		bodyBytes, err = json.Marshal(resp)
		if err != nil {
			log.G(Ctx).Error(err)
			statusCode = http.StatusInternalServerError
			w.WriteHeader(statusCode)
			w.Write([]byte("Some errors occurred while checking container status. Check Docker Sidecar's logs"))
		}
		w.Write(bodyBytes)
	}
}
