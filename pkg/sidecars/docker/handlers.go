package docker

import (
	"encoding/json"
	"io/ioutil"
	"net/http"
	"os"
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

	bodyBytes, err := ioutil.ReadAll(r.Body)
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
			w.Write([]byte("Some errors occurred while checking container status. Check Docker Sidecar's logs"))
			w.WriteHeader(statusCode)
		}
		w.Write(bodyBytes)
	}
}

func CreateHandler(w http.ResponseWriter, r *http.Request) {
	log.G(Ctx).Info("Docker Sidecar: received Create call")
	var execReturn exec.ExecResult
	statusCode := http.StatusOK
	bodyBytes, err := ioutil.ReadAll(r.Body)
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		w.Write([]byte("Some errors occurred while creating container. Check Docker Sidecar's logs"))
		log.G(Ctx).Error(err)
		return
	}

	var req []commonIL.RetrievedPodData
	err = json.Unmarshal(bodyBytes, &req)

	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		w.Write([]byte("Some errors occurred while creating container. Check Docker Sidecar's logs"))
		log.G(Ctx).Error(err)
		return
	}

	for _, data := range req {
		for _, container := range data.Pod.Spec.Containers {
			log.G(Ctx).Info("- Creating container " + container.Name)
			cmd := []string{"run", "-d", "--name", container.Name}

			if commonIL.InterLinkConfigInst.ExportPodData {
				mounts, err := prepare_mounts(container, req)
				if err != nil {
					statusCode = http.StatusInternalServerError
					log.G(Ctx).Error(err)
					w.WriteHeader(statusCode)
					w.Write([]byte("Some errors occurred while creating container. Check Docker Sidecar's logs"))
					os.RemoveAll(commonIL.InterLinkConfigInst.DataRootFolder + data.Pod.Namespace + "-" + string(data.Pod.UID))
					return
				}
				cmd = append(cmd, mounts)
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
				statusCode = http.StatusInternalServerError
				log.G(Ctx).Error(err)
				w.WriteHeader(statusCode)
				w.Write([]byte("Some errors occurred while creating container. Check Docker Sidecar's logs"))
				os.RemoveAll(commonIL.InterLinkConfigInst.DataRootFolder + data.Pod.Namespace + "-" + string(data.Pod.UID))
				return
			}

			if execReturn.Stdout == "" {
				eval := "Conflict. The container name \"/" + container.Name + "\" is already in use"
				if strings.Contains(execReturn.Stderr, eval) {
					log.G(Ctx).Warning("Container named " + container.Name + " already exists. Skipping its creation.")
				} else {
					statusCode = http.StatusInternalServerError
					log.G(Ctx).Error("Unable to create container " + container.Name + " : " + execReturn.Stderr)
					w.WriteHeader(statusCode)
					w.Write([]byte("Some errors occurred while creating container. Check Docker Sidecar's logs"))
					os.RemoveAll(commonIL.InterLinkConfigInst.DataRootFolder + data.Pod.Namespace + "-" + string(data.Pod.UID))
					return
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
				statusCode = http.StatusInternalServerError
				log.G(Ctx).Error("Failed to retrieve " + container.Name + " ID : " + execReturn.Stderr)
				w.WriteHeader(statusCode)
				w.Write([]byte("Some errors occurred while creating container. Check Docker Sidecar's logs"))
				os.RemoveAll(commonIL.InterLinkConfigInst.DataRootFolder + data.Pod.Namespace + "-" + string(data.Pod.UID))
				return
			} else if execReturn.Stdout == "" {
				log.G(Ctx).Error("Container name not found. Maybe creation failed?")
			} else {
				log.G(Ctx).Debug("-- Retrieved " + container.Name + " ID: " + execReturn.Stdout)
			}
		}
	}

	w.WriteHeader(statusCode)

	if statusCode != http.StatusOK {
		w.Write([]byte("Some errors occurred while creating containers. Check Docker Sidecar's logs"))
	} else {
		w.Write([]byte("Containers created"))
	}
}

func DeleteHandler(w http.ResponseWriter, r *http.Request) {
	log.G(Ctx).Info("Docker Sidecar: received Delete call")
	var execReturn exec.ExecResult
	statusCode := http.StatusOK
	bodyBytes, err := ioutil.ReadAll(r.Body)

	if err != nil {
		statusCode = http.StatusInternalServerError
		log.G(Ctx).Error(err)
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
		log.G(Ctx).Error(err)
		return
	}

	for _, pod := range req {
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
				if strings.Contains(execReturn.Stderr, "No such container") {
					log.G(Ctx).Debug("-- Unable to find container " + container.Name + ". Probably already removed? Skipping its removal")
				} else {
					log.G(Ctx).Error("-- Error stopping container " + container.Name + ". Skipping its removal")
					statusCode = http.StatusInternalServerError
					w.WriteHeader(statusCode)
					w.Write([]byte("Some errors occurred while deleting container. Check Docker Sidecar's logs"))
					return
				}
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
				statusCode = http.StatusInternalServerError
				w.WriteHeader(statusCode)
				w.Write([]byte("Some errors occurred while deleting container. Check Docker Sidecar's logs"))
				return
			} else {
				log.G(Ctx).Info("- Deleted container " + container.Name)
			}

			os.RemoveAll(commonIL.InterLinkConfigInst.DataRootFolder + pod.Namespace + "-" + string(pod.UID))
		}
	}

	w.WriteHeader(statusCode)
	if statusCode != http.StatusOK {
		w.Write([]byte("Some errors occurred deleting containers. Check Docker Sidecar's logs"))
	} else {
		w.Write([]byte("All containers for submitted Pods have been deleted"))
	}
}
