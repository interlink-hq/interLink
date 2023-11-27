package docker

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"

	exec "github.com/alexellis/go-execute/pkg/v1"
	"github.com/containerd/containerd/log"
	commonIL "github.com/intertwin-eu/interlink/pkg/common"
)

func CreateHandler(w http.ResponseWriter, r *http.Request) {
	log.G(Ctx).Info("Docker Sidecar: received Create call")
	var execReturn exec.ExecResult
	statusCode := http.StatusOK
	bodyBytes, err := io.ReadAll(r.Body)
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

			docker_options := ""

			if docker_flags, ok := data.Pod.ObjectMeta.Annotations["docker-options.vk.io/flags"]; ok {
				parsed_docker_options := strings.Split(docker_flags, " ")
				if parsed_docker_options != nil {
					for _, option := range parsed_docker_options {
						docker_options += " " + option
					}
				}
			}

			shell := exec.ExecTask{
				Command: "docker" + docker_options,
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
