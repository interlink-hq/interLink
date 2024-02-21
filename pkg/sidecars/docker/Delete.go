package docker

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"

	exec "github.com/alexellis/go-execute/pkg/v1"
	"github.com/containerd/containerd/log"
	v1 "k8s.io/api/core/v1"
)

// DeleteHandler stops and deletes Docker containers from provided data
func (h *SidecarHandler) DeleteHandler(w http.ResponseWriter, r *http.Request) {
	log.G(h.Ctx).Info("Docker Sidecar: received Delete call")
	var execReturn exec.ExecResult
	statusCode := http.StatusOK
	bodyBytes, err := io.ReadAll(r.Body)

	if err != nil {
		statusCode = http.StatusInternalServerError
		log.G(h.Ctx).Error(err)
		w.WriteHeader(statusCode)
		w.Write([]byte("Some errors occurred while deleting container. Check Docker Sidecar's logs"))
		return
	}

	var req []*v1.Pod
	err = json.Unmarshal(bodyBytes, &req)
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		w.Write([]byte("Some errors occurred while creating container. Check Docker Sidecar's logs"))
		log.G(h.Ctx).Error(err)
		return
	}

	for _, pod := range req {
		for _, container := range pod.Spec.Containers {
			log.G(h.Ctx).Debug("- Deleting container " + container.Name)
			cmd := []string{"stop", container.Name}
			shell := exec.ExecTask{
				Command: "docker",
				Args:    cmd,
				Shell:   true,
			}
			execReturn, _ = shell.Execute()

			if execReturn.Stderr != "" {
				if strings.Contains(execReturn.Stderr, "No such container") {
					log.G(h.Ctx).Debug("-- Unable to find container " + container.Name + ". Probably already removed? Skipping its removal")
				} else {
					log.G(h.Ctx).Error("-- Error stopping container " + container.Name + ". Skipping its removal")
					statusCode = http.StatusInternalServerError
					w.WriteHeader(statusCode)
					w.Write([]byte("Some errors occurred while deleting container. Check Docker Sidecar's logs"))
					return
				}
				continue
			}

			if execReturn.Stdout != "" {
				cmd = []string{"rm", execReturn.Stdout}
				shell = exec.ExecTask{
					Command: "docker",
					Args:    cmd,
					Shell:   true,
				}
				execReturn, _ = shell.Execute()
				execReturn.Stdout = strings.ReplaceAll(execReturn.Stdout, "\n", "")

				if execReturn.Stderr != "" {
					log.G(h.Ctx).Error("-- Error deleting container " + container.Name)
					statusCode = http.StatusInternalServerError
					w.WriteHeader(statusCode)
					w.Write([]byte("Some errors occurred while deleting container. Check Docker Sidecar's logs"))
					return
				} else {
					log.G(h.Ctx).Info("- Deleted container " + container.Name)
				}
			}

			os.RemoveAll(h.Config.DataRootFolder + pod.Namespace + "-" + string(pod.UID))
		}
	}

	w.WriteHeader(statusCode)
	if statusCode != http.StatusOK {
		w.Write([]byte("Some errors occurred deleting containers. Check Docker Sidecar's logs"))
	} else {
		w.Write([]byte("All containers for submitted Pods have been deleted"))
	}
}
