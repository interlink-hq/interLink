package slurm

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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var JID []string

func SubmitHandler(w http.ResponseWriter, r *http.Request) {
	log.G(Ctx).Info("Slurm Sidecar: received Submit call")
	statusCode := http.StatusOK
	bodyBytes, err := ioutil.ReadAll(r.Body)
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

			path, err := produce_slurm_script(container, metadata, singularity_command)
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
			err = handle_jid(container, out, data.Pod)
			if err != nil {
				statusCode = http.StatusInternalServerError
				w.WriteHeader(statusCode)
				w.Write([]byte("Error handling JID. Check Slurm Sidecar's logs"))
				log.G(Ctx).Error(err)
				os.RemoveAll(commonIL.InterLinkConfigInst.DataRootFolder + data.Pod.Namespace + "-" + string(data.Pod.UID))
				return
			}

			jid, err := os.ReadFile(commonIL.InterLinkConfigInst.DataRootFolder + container.Name + ".jid")
			if err != nil {
				statusCode = http.StatusInternalServerError
				w.WriteHeader(statusCode)
				w.Write([]byte("Some errors occurred while creating container. Check Slurm Sidecar's logs"))
				log.G(Ctx).Error(err)
				os.RemoveAll(commonIL.InterLinkConfigInst.DataRootFolder + data.Pod.Namespace + "-" + string(data.Pod.UID))
				return
			}
			JID = append(JID, string(jid))
		}
	}

	w.WriteHeader(statusCode)

	if statusCode != http.StatusOK {
		w.Write([]byte("Some errors occurred while creating containers. Check Slurm Sidecar's logs"))
	} else {
		w.Write([]byte("Containers created"))
	}
}

func StopHandler(w http.ResponseWriter, r *http.Request) {
	log.G(Ctx).Info("Slurm Sidecar: received Stop call")
	statusCode := http.StatusOK

	bodyBytes, err := ioutil.ReadAll(r.Body)
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		w.Write([]byte("Some errors occurred while deleting container. Check Slurm Sidecar's logs"))
		log.G(Ctx).Error(err)
		return
	}

	var req []*v1.Pod
	err = json.Unmarshal(bodyBytes, &req)
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		w.Write([]byte("Some errors occurred while deleting container. Check Slurm Sidecar's logs"))
		log.G(Ctx).Error(err)
		return
	}

	for _, pod := range req {
		containers := pod.Spec.Containers

		for _, container := range containers {
			err = delete_container(container)
			if err != nil {
				statusCode = http.StatusInternalServerError
				w.WriteHeader(statusCode)
				w.Write([]byte("Error deleting containers. Check Slurm Sidecar's logs"))
				log.G(Ctx).Error(err)
				return
			}
			if os.Getenv("SHARED_FS") != "true" {
				err = os.RemoveAll(commonIL.InterLinkConfigInst.DataRootFolder + pod.Namespace + "-" + string(pod.UID))
			}
		}
	}

	w.WriteHeader(statusCode)
	if statusCode != http.StatusOK {
		w.Write([]byte("Some errors occurred deleting containers. Check Slurm Sidecar's logs"))
	} else {
		w.Write([]byte("All containers for submitted Pods have been deleted"))
	}
}

func StatusHandler(w http.ResponseWriter, r *http.Request) {
	var req []*v1.Pod
	var resp []commonIL.PodStatus
	statusCode := http.StatusOK
	log.G(Ctx).Info("Slurm Sidecar: received GetStatus call")

	bodyBytes, err := ioutil.ReadAll(r.Body)
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

	bodyBytes, err = json.Marshal(resp)
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		w.Write([]byte("Some errors occurred while retrieving container status. Check Slurm Sidecar's logs"))
		log.G(Ctx).Error("Unable to retrieve job status: " + execReturn.Stderr)
		log.G(Ctx).Error(err)
		return
	}

	w.WriteHeader(statusCode)
	if statusCode != http.StatusOK {
		w.Write([]byte("Some errors occurred deleting containers. Check Docker Sidecar's logs"))
	} else {
		w.Write([]byte("All containers for submitted Pods have been deleted"))
	}
}
