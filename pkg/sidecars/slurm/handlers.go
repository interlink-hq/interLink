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

var JID []JidStruct

func SubmitHandler(w http.ResponseWriter, r *http.Request) {
	log.G(Ctx).Info("Slurm Sidecar: received Submit call")
	//var resp commonIL.StatusResponse

	bodyBytes, err := ioutil.ReadAll(r.Body)
	if err != nil {
		log.G(Ctx).Error(err)
	}

	var req []commonIL.RetrievedPodData
	json.Unmarshal(bodyBytes, &req)

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
			mounts := prepare_mounts(container, &data.Pod, req)
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

			path := produce_slurm_script(container, metadata, singularity_command)
			/*out := */ slurm_batch_submit(path)
			//handle_jid(container, out, data.Pod)

			jid, err := os.ReadFile(commonIL.InterLinkConfigInst.DataRootFolder + container.Name + ".jid")
			if err != nil {
				log.G(Ctx).Error("Unable to read JID from file")
			}
			JID = append(JID, JidStruct{JID: string(jid), Pod: data.Pod})
		}
	}

	w.Write([]byte(nil))
}

func StopHandler(w http.ResponseWriter, r *http.Request) {
	log.G(Ctx).Info("Slurm Sidecar: received Stop call")

	bodyBytes, err := ioutil.ReadAll(r.Body)
	if err != nil {
		log.G(Ctx).Error(err)
		return
	}

	var req []*v1.Pod
	err = json.Unmarshal(bodyBytes, &req)
	if err != nil {
		log.G(Ctx).Error(err)
		return
	}

	for _, pod := range req {
		containers := pod.Spec.Containers

		for _, container := range containers {
			delete_container(container)
		}
	}
}

func StatusHandler(w http.ResponseWriter, r *http.Request) {
	log.G(Ctx).Info("Slurm Sidecar: received GetStatus call")

	bodyBytes, err := ioutil.ReadAll(r.Body)
	if err != nil {
		log.G(Ctx).Error(err)
		return
	}

	var req []*v1.Pod
	var resp commonIL.StatusResponse
	json.Unmarshal(bodyBytes, &req)
	if err != nil {
		log.G(Ctx).Error(err)
		return
	}

	cmd := []string{"--me"}
	shell := exec.ExecTask{
		Command: "squeue",
		Args:    cmd,
		Shell:   true,
	}
	execReturn, err := shell.Execute()
	execReturn.Stdout = strings.ReplaceAll(execReturn.Stdout, "\n", "")

	if execReturn.Stderr != "" {
		log.G(Ctx).Error("Unable to retrieve job status: " + execReturn.Stderr)
	}

	for _, pod := range req {
		var flag = false
		for _, jid := range JID {

			cmd := []string{"-c", "squeue --me | grep " + jid.JID}
			shell := exec.ExecTask{
				Command: "bash",
				Args:    cmd,
				Shell:   true,
			}
			execReturn, _ := shell.Execute()

			if execReturn.Stderr != "" {
				log.G(Ctx).Error("Unable to retrieve job status: " + execReturn.Stderr)
			} else if execReturn.Stdout != "" {
				flag = true
				log.G(Ctx).Info(execReturn.Stdout)
			}
		}

		if flag {
			resp.PodStatus = append(resp.PodStatus, commonIL.PodStatus{PodName: string(pod.Name), PodStatus: commonIL.RUNNING})
		} else {
			resp.PodStatus = append(resp.PodStatus, commonIL.PodStatus{PodName: string(pod.Name), PodStatus: commonIL.STOP})
		}
	}
	resp.ReturnVal = "Status"

	bodyBytes, _ = json.Marshal(resp)

	w.Write(bodyBytes)
}
