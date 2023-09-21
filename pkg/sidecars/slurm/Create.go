package slurm

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/containerd/containerd/log"
	commonIL "github.com/intertwin-eu/interlink/pkg/common"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

	for _, data := range req {
		var metadata metav1.ObjectMeta
		var containers []v1.Container

		containers = data.Pod.Spec.Containers
		metadata = data.Pod.ObjectMeta

		for _, container := range containers {
			log.G(Ctx).Info("- Beginning script generation for container " + container.Name)
			commstr1 := []string{"singularity", "exec"}

			envs := prepare_envs(container)
			image := ""
			mounts, err := prepare_mounts(container, req)
			if err != nil {
				statusCode = http.StatusInternalServerError
				w.WriteHeader(statusCode)
				w.Write([]byte("Error prepairing mounts. Check Slurm Sidecar's logs"))
				log.G(Ctx).Error(err)
				os.RemoveAll(commonIL.InterLinkConfigInst.DataRootFolder + data.Pod.Namespace + "-" + string(data.Pod.UID))
				return
			}

			if strings.HasPrefix(container.Image, "/") {
				if image_uri, ok := metadata.Annotations["slurm-job.knoc.io/image-root"]; ok {
					image = image_uri + container.Image
				} else {
					log.G(Ctx).Info("- image-uri annotation not specified for path in remote filesystem")
				}
			} else {
				image = "docker://" + container.Image
			}
			image = container.Image

			log.G(Ctx).Debug("-- Appending all commands together...")
			singularity_command := append(commstr1, envs...)
			singularity_command = append(singularity_command, mounts...)
			singularity_command = append(singularity_command, image)
			singularity_command = append(singularity_command, container.Command...)
			singularity_command = append(singularity_command, container.Args...)

			path, err := produce_slurm_script(container, string(data.Pod.UID), metadata, singularity_command)
			if err != nil {
				statusCode = http.StatusInternalServerError
				w.WriteHeader(statusCode)
				w.Write([]byte("Error producing Slurm script. Check Slurm Sidecar's logs"))
				log.G(Ctx).Error(err)
				os.RemoveAll(commonIL.InterLinkConfigInst.DataRootFolder + data.Pod.Namespace + "-" + string(data.Pod.UID))
				return
			}
			out, err := slurm_batch_submit(path)
			if err != nil {
				statusCode = http.StatusInternalServerError
				w.WriteHeader(statusCode)
				w.Write([]byte("Error submitting Slurm script. Check Slurm Sidecar's logs"))
				log.G(Ctx).Error(err)
				os.RemoveAll(commonIL.InterLinkConfigInst.DataRootFolder + data.Pod.Namespace + "-" + string(data.Pod.UID))
				return
			}
			err = handle_jid(container, string(data.Pod.UID), out, data.Pod)
			if err != nil {
				statusCode = http.StatusInternalServerError
				w.WriteHeader(statusCode)
				w.Write([]byte("Error handling JID. Check Slurm Sidecar's logs"))
				log.G(Ctx).Error(err)
				os.RemoveAll(commonIL.InterLinkConfigInst.DataRootFolder + data.Pod.Namespace + "-" + string(data.Pod.UID))
				err = delete_container(container, string(data.Pod.UID))
				return
			}

			jid, err := os.ReadFile(commonIL.InterLinkConfigInst.DataRootFolder + string(data.Pod.UID) + "_" + container.Name + ".jid")
			if err != nil {
				statusCode = http.StatusInternalServerError
				w.WriteHeader(statusCode)
				w.Write([]byte("Some errors occurred while creating container. Check Slurm Sidecar's logs"))
				log.G(Ctx).Error(err)
				os.RemoveAll(commonIL.InterLinkConfigInst.DataRootFolder + data.Pod.Namespace + "-" + string(data.Pod.UID))
				return
			}

			flag := true
			for _, JID := range JIDs {
				if JID.PodName == data.Pod.Name {
					flag = false
					JID.JIDs = append(JID.JIDs, string(jid))
				}
			}
			if flag {
				JIDs = append(JIDs, commonIL.JidStruct{PodName: data.Pod.Name, JIDs: []string{string(jid)}})
			}
		}
	}

	w.WriteHeader(statusCode)

	if statusCode != http.StatusOK {
		w.Write([]byte("Some errors occurred while creating containers. Check Slurm Sidecar's logs"))
	} else {
		w.Write([]byte("Containers created"))
	}
}
