package docker

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	exec "github.com/alexellis/go-execute/pkg/v1"
	"github.com/containerd/containerd/log"
	v1 "k8s.io/api/core/v1"

	commonIL "github.com/intertwin-eu/interlink/pkg/common"
)

// StatusHandler checks Docker Container's status by running docker ps -af command and returns that status
func (h *SidecarHandler) StatusHandler(w http.ResponseWriter, r *http.Request) {
	log.G(h.Ctx).Info("Docker Sidecar: received GetStatus call")
	var resp []commonIL.PodStatus
	var req []*v1.Pod
	statusCode := http.StatusOK

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		w.Write([]byte("Some errors occurred while checking container status. Check Docker Sidecar's logs"))
		log.G(h.Ctx).Error(err)
		return
	}

	err = json.Unmarshal(bodyBytes, &req)
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		w.Write([]byte("Some errors occurred while checking container status. Check Docker Sidecar's logs"))
		log.G(h.Ctx).Error(err)
		return
	}

	for i, pod := range req {
		resp = append(resp, commonIL.PodStatus{PodName: pod.Name, PodUID: string(pod.UID), PodNamespace: pod.Namespace})
		for _, container := range pod.Spec.Containers {
			log.G(h.Ctx).Debug("- Getting status for container " + container.Name)
			cmd := []string{"ps -af name=^" + container.Name + "$ --format \"{{.Status}}\""}

			shell := exec.ExecTask{
				Command: "docker",
				Args:    cmd,
				Shell:   true,
			}
			execReturn, err := shell.Execute()
			execReturn.Stdout = strings.ReplaceAll(execReturn.Stdout, "\n", "")

			if err != nil {
				log.G(h.Ctx).Error(err)
				statusCode = http.StatusInternalServerError
				break
			}

			containerstatus := strings.Split(execReturn.Stdout, " ")

			// TODO: why first container?
			if execReturn.Stdout != "" {
				if containerstatus[0] == "Created" {
					log.G(h.Ctx).Info("-- Container " + container.Name + " is going ready...")
					resp[i].Containers = append(resp[i].Containers, v1.ContainerStatus{Name: container.Name, State: v1.ContainerState{Waiting: &v1.ContainerStateWaiting{}}, Ready: false})
				} else if containerstatus[0] == "Up" {
					log.G(h.Ctx).Info("-- Container " + container.Name + " is running")
					resp[i].Containers = append(resp[i].Containers, v1.ContainerStatus{Name: container.Name, State: v1.ContainerState{Running: &v1.ContainerStateRunning{}}, Ready: true})
				} else if containerstatus[0] == "Exited" {
					log.G(h.Ctx).Info("-- Container " + container.Name + " has been stopped")
					resp[i].Containers = append(resp[i].Containers, v1.ContainerStatus{Name: container.Name, State: v1.ContainerState{Terminated: &v1.ContainerStateTerminated{}}, Ready: false})
				}
			} else {
				log.G(h.Ctx).Info("-- Container " + container.Name + " doesn't exist")
				resp[i].Containers = append(resp[i].Containers, v1.ContainerStatus{Name: container.Name, State: v1.ContainerState{Terminated: &v1.ContainerStateTerminated{}}, Ready: false})
			}
		}
	}

	w.WriteHeader(statusCode)

	if statusCode != http.StatusOK {
		w.Write([]byte("Some errors occurred while checking container status. Check Docker Sidecar's logs"))
	} else {
		bodyBytes, err = json.Marshal(resp)
		if err != nil {
			log.G(h.Ctx).Error(err)
			statusCode = http.StatusInternalServerError
			w.WriteHeader(statusCode)
			w.Write([]byte("Some errors occurred while checking container status. Check Docker Sidecar's logs"))
		}
		w.Write(bodyBytes)
	}
}
