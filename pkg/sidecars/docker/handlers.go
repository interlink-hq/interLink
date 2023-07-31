package docker

import (
	"encoding/json"
	"io/ioutil"
	"net/http"
	"strings"

	exec "github.com/alexellis/go-execute/pkg/v1"
	"github.com/containerd/containerd/log"
	commonIL "github.com/intertwin-eu/interlink/pkg/common"
)

func StatusHandler(w http.ResponseWriter, r *http.Request) {
	log.G(Ctx).Info("Docker Sidecar: received GetStatus call")
	var resp commonIL.StatusResponse

	bodyBytes, err := ioutil.ReadAll(r.Body)
	if err != nil {
		log.G(Ctx).Error(err)
	}

	var req commonIL.Request
	json.Unmarshal(bodyBytes, &req)

	for _, pod := range req.Pods {
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
				return
			}

			if execReturn.Stdout == "" {
				log.G(Ctx).Info("-- Container " + container.Name + " is not running")
				resp.PodStatus = append(resp.PodStatus, commonIL.PodStatus{PodName: pod.Name, PodStatus: commonIL.STOP})
			} else {
				log.G(Ctx).Info("-- Container " + container.Name + " is running")
				resp.PodStatus = append(resp.PodStatus, commonIL.PodStatus{PodName: pod.Name, PodStatus: commonIL.RUNNING})
			}
		}
	}

	resp.ReturnVal = "Status"
	bodyBytes, _ = json.Marshal(resp)

	w.Write(bodyBytes)
}

func CreateHandler(w http.ResponseWriter, r *http.Request) {
	log.G(Ctx).Info("Docker Sidecar: received Create call")
	var execReturn exec.ExecResult
	bodyBytes, err := ioutil.ReadAll(r.Body)
	if err != nil {
		log.G(Ctx).Error(err)
	}

	var req []commonIL.RetrievedPodData
	json.Unmarshal(bodyBytes, &req)

	for _, data := range req {
		for _, container := range data.Pod.Spec.Containers {
			log.G(Ctx).Info("- Creating container " + container.Name)
			cmd := []string{"run", "-d", "--name", container.Name}

			if commonIL.InterLinkConfigInst.ExportPodData {
				cmd = append(cmd, prepare_mounts(container, data))
			}

			cmd = append(cmd, container.Image)

			for _, command := range container.Command {
				cmd = append(cmd, command)
			}
			for _, args := range container.Args {
				cmd = append(cmd, args)
			}

			shell := exec.ExecTask{
				Command: "docker",
				Args:    cmd,
				Shell:   true,
			}

			execReturn, err = shell.Execute()
			if err != nil {
				log.G(Ctx).Error(err)
				return
			}

			if execReturn.Stdout == "" {
				eval := "Conflict. The container name \"/" + container.Name + "\" is already in use"
				if strings.Contains(execReturn.Stderr, eval) {
					log.G(Ctx).Warning("Container named " + container.Name + " already exists. Skipping its creation.")
				} else {
					log.G(Ctx).Error("Unable to create container " + container.Name + " : " + execReturn.Stderr)
				}
			} else {
				log.G(Ctx).Info("-- Created container " + container.Name)
			}

			shell = exec.ExecTask{
				Command: "docker",
				Args:    []string{"ps", "-aqf", "name=^" + container.Name + "$"},
				Shell:   true,
			}

			execReturn, err = shell.Execute()
			execReturn.Stdout = strings.ReplaceAll(execReturn.Stdout, "\n", "")
			if execReturn.Stderr != "" {
				log.G(Ctx).Error("Failed to retrieve " + container.Name + " ID : " + execReturn.Stderr)
			} else if execReturn.Stdout == "" {
				log.G(Ctx).Error("Container name not found. Maybe creation failed?")
			} else {
				log.G(Ctx).Debug("-- Retrieved " + container.Name + " ID: " + execReturn.Stdout)
			}
		}
	}

	if err != nil {
		w.Write([]byte(execReturn.Stderr))
	} else {
		w.Write([]byte("200"))
	}
}

func DeleteHandler(w http.ResponseWriter, r *http.Request) {
	log.G(Ctx).Info("Docker Sidecar: received Delete call")
	var execReturn exec.ExecResult
	bodyBytes, err := ioutil.ReadAll(r.Body)
	if err != nil {
		log.G(Ctx).Error(err)
	}

	var req commonIL.Request
	json.Unmarshal(bodyBytes, &req)

	for _, pod := range req.Pods {
		for _, container := range pod.Spec.Containers {
			log.G(Ctx).Debug("- Deleting container " + container.Name)
			cmd := []string{"stop", container.Name}
			shell := exec.ExecTask{
				Command: "docker",
				Args:    cmd,
				Shell:   true,
			}
			execReturn, _ = shell.Execute()

			if execReturn.Stderr != "" {
				log.G(Ctx).Error("-- Error stopping container " + container.Name + ". Skipping its removal")
				continue
			}

			cmd = []string{"rm", execReturn.Stdout}
			shell = exec.ExecTask{
				Command: "docker",
				Args:    cmd,
				Shell:   true,
			}
			execReturn, _ = shell.Execute()
			execReturn.Stdout = strings.ReplaceAll(execReturn.Stdout, "\n", "")

			if execReturn.Stderr != "" {
				log.G(Ctx).Error("-- Error deleting container " + container.Name)
			} else {
				log.G(Ctx).Info("- Deleted container " + container.Name)
			}
		}
	}

	if err != nil {
		w.Write([]byte(execReturn.Stderr))
	} else {
		w.Write([]byte("All containers for submitted Pods have been deleted"))
	}
}
