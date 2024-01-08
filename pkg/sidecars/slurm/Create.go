package slurm

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/containerd/containerd/log"
	commonIL "github.com/intertwin-eu/interlink/pkg/common"
)

func SubmitHandler(w http.ResponseWriter, r *http.Request) {
	log.G(Ctx).Info("Slurm Sidecar: received Submit call")
	statusCode := http.StatusOK
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		w.Write([]byte("Some errors occurred while creating container. Check Slurm Sidecar's logs"))
		log.G(Ctx).Error(err)
		return
	}

	var req []commonIL.RetrievedPodData
	err = json.Unmarshal(bodyBytes, &req)
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		w.Write([]byte("Some errors occurred while creating container. Check Slurm Sidecar's logs"))
		log.G(Ctx).Error(err)
		return
	}

	for _, test := range req {
		log.G(Ctx).Debug(test.Pod.UID)
	}

	for _, data := range req {
		containers := data.Pod.Spec.Containers
		metadata := data.Pod.ObjectMeta
		filesPath := commonIL.InterLinkConfigInst.DataRootFolder + data.Pod.Namespace + "-" + string(data.Pod.UID)

		var singularity_command_pod []SingularityCommand

		for _, container := range containers {
			log.G(Ctx).Info("- Beginning script generation for container " + container.Name)
			singularityPrefix := commonIL.InterLinkConfigInst.SingularityPrefix
			if singularityAnnotation, ok := metadata.Annotations["job.vk.io/singularity-commands"]; ok {
				singularityPrefix += " " + singularityAnnotation
			}
			commstr1 := []string{"singularity", "exec", "--writable-tmpfs", "--nv", "-H", "${HOME}/" +
				commonIL.InterLinkConfigInst.DataRootFolder + string(data.Pod.UID) + ":${HOME}"}

			envs := prepare_envs(container)
			image := ""
			mounts, err := prepare_mounts(filesPath, container, req)
			log.G(Ctx).Debug(mounts)
			if err != nil {
				statusCode = http.StatusInternalServerError
				w.WriteHeader(statusCode)
				w.Write([]byte("Error prepairing mounts. Check Slurm Sidecar's logs"))
				log.G(Ctx).Error(err)
				os.RemoveAll(filesPath)
				return
			}

			image = container.Image
			if strings.HasPrefix(container.Image, "/") {
				if image_uri, ok := metadata.Annotations["slurm-job.vk.io/image-root"]; ok {
					image = image_uri + container.Image
				} else {
					log.G(Ctx).Info("- image-uri annotation not specified for path in remote filesystem")
				}
			} else {
				image = container.Image
			}

			log.G(Ctx).Debug("-- Appending all commands together...")
			singularity_command := append(commstr1, envs...)
			singularity_command = append(singularity_command, mounts...)
			singularity_command = append(singularity_command, image)
			singularity_command = append(singularity_command, container.Command...)
			singularity_command = append(singularity_command, container.Args...)

			singularity_command_pod = append(singularity_command_pod, SingularityCommand{command: singularity_command, containerName: container.Name})
		}

		path, err := produce_slurm_script(filesPath, data.Pod.Namespace, string(data.Pod.UID), metadata, singularity_command_pod)
		if err != nil {
			statusCode = http.StatusInternalServerError
			w.WriteHeader(statusCode)
			w.Write([]byte("Error producing Slurm script. Check Slurm Sidecar's logs"))
			log.G(Ctx).Error(err)
			os.RemoveAll(filesPath)
			return
		}
		out, err := slurm_batch_submit(path)
		if err != nil {
			statusCode = http.StatusInternalServerError
			w.WriteHeader(statusCode)
			w.Write([]byte("Error submitting Slurm script. Check Slurm Sidecar's logs"))
			log.G(Ctx).Error(err)
			os.RemoveAll(filesPath)
			return
		}
		log.G(Ctx).Info(out)
		err = handle_jid(string(data.Pod.UID), out, data.Pod, filesPath)
		if err != nil {
			statusCode = http.StatusInternalServerError
			w.WriteHeader(statusCode)
			w.Write([]byte("Error handling JID. Check Slurm Sidecar's logs"))
			log.G(Ctx).Error(err)
			os.RemoveAll(filesPath)
			err = delete_container(string(data.Pod.UID), filesPath)
			return
		}
	}

	w.WriteHeader(statusCode)

	if statusCode != http.StatusOK {
		w.Write([]byte("Some errors occurred while creating containers. Check Slurm Sidecar's logs"))
	} else {
		w.Write([]byte("Containers created"))
	}
}
