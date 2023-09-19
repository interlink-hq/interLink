package slurm

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
	var req []*v1.Pod
	var resp []commonIL.PodStatus
	statusCode := http.StatusOK
	log.G(Ctx).Info("Slurm Sidecar: received GetStatus call")

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		w.Write([]byte("Some errors occurred while retrieving container status. Check Slurm Sidecar's logs"))
		log.G(Ctx).Error(err)
		return
	}

	json.Unmarshal(bodyBytes, &req)
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		w.Write([]byte("Some errors occurred while retrieving container status. Check Slurm Sidecar's logs"))
		log.G(Ctx).Error(err)
		return
	}

	cmd := []string{"--me"}
	shell := exec.ExecTask{
		Command: "squeue",
		Args:    cmd,
		Shell:   true,
	}
	execReturn, _ := shell.Execute()
	execReturn.Stdout = strings.ReplaceAll(execReturn.Stdout, "\n", "")

	if execReturn.Stderr != "" {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		w.Write([]byte("Error executing Squeue. Check Slurm Sidecar's logs"))
		log.G(Ctx).Error("Unable to retrieve job status: " + execReturn.Stderr)
		return
	}

	for _, pod := range req {
		var flag = false
		for _, jid := range JID {

			cmd := []string{"-c", "squeue --me | grep " + jid}
			shell := exec.ExecTask{
				Command: "bash",
				Args:    cmd,
				Shell:   true,
			}
			execReturn, _ := shell.Execute()

			if execReturn.Stderr != "" {
				statusCode = http.StatusInternalServerError
				w.WriteHeader(statusCode)
				w.Write([]byte("Error executing Squeue. Check Slurm Sidecar's logs"))
				log.G(Ctx).Error("Unable to retrieve job status: " + execReturn.Stderr)
				return
			} else if execReturn.Stdout != "" {
				flag = true
				log.G(Ctx).Info(execReturn.Stdout)
			}
		}

		if flag {
			resp = append(resp, commonIL.PodStatus{PodName: pod.Name, PodNamespace: pod.Namespace, PodStatus: commonIL.RUNNING})
		} else {
			resp = append(resp, commonIL.PodStatus{PodName: pod.Name, PodNamespace: pod.Namespace, PodStatus: commonIL.STOP})
		}
	}

	w.WriteHeader(statusCode)
	if statusCode != http.StatusOK {
		w.Write([]byte("Some errors occurred deleting containers. Check Docker Sidecar's logs"))
	} else {
		bodyBytes, err = json.Marshal(resp)
		if err != nil {
			w.WriteHeader(statusCode)
			w.Write([]byte("Some errors occurred while retrieving container status. Check Slurm Sidecar's logs"))
			log.G(Ctx).Error("Unable to retrieve job status: " + execReturn.Stderr)
			log.G(Ctx).Error(err)
			return
		}
		w.Write(bodyBytes)
	}
}
