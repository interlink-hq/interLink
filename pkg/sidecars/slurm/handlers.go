package slurm

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"

	exec "github.com/alexellis/go-execute/pkg/v1"
	commonIL "github.com/cloud-pg/interlink/pkg/common"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var JID []JidStruct

func SubmitHandler(w http.ResponseWriter, r *http.Request) {
	//call to slurm create container

	bodyBytes, err := ioutil.ReadAll(r.Body)
	if err != nil {
		log.Print(err)
		return
	}

	var req commonIL.Request
	json.Unmarshal(bodyBytes, &req)
	if err != nil {
		log.Print(err)
		return
	}

	for _, pod := range req.Pods {
		var metadata metav1.ObjectMeta
		var containers []v1.Container

		containers = pod.Spec.Containers
		metadata = pod.ObjectMeta

		for _, container := range containers {
			log.Print("create_container")
			commstr1 := []string{"singularity", "exec"}

			envs := prepare_envs(container)
			image := ""
			mounts := prepare_mounts(container, pod)
			if strings.HasPrefix(container.Image, "/") {
				if image_uri, ok := metadata.Annotations["slurm-job.knoc.io/image-root"]; ok {
					log.Print(image_uri)
					image = image_uri + container.Image
				} else {
					log.Print("image-uri annotation not specified for path in remote filesystem")
				}
			} else {
				image = "docker://" + container.Image
			}
			image = container.Image

			singularity_command := append(commstr1, envs...)
			singularity_command = append(singularity_command, mounts...)
			singularity_command = append(singularity_command, image)
			singularity_command = append(singularity_command, container.Command...)
			singularity_command = append(singularity_command, container.Args...)

			log.Println("Generating Slurm script")
			path := produce_slurm_script(container, metadata, singularity_command)
			log.Println("Submitting Slurm job")
			out := slurm_batch_submit(path)
			handle_jid(container, out, *pod)
			log.Print(out)

			jid, err := os.ReadFile(".knoc/" + container.Name + ".jid")
			if err != nil {
				log.Println("Unable to read JID from file")
			}
			JID = append(JID, JidStruct{JID: string(jid), Pod: *pod})
		}
	}

	w.Write([]byte(nil))
}

func StopHandler(w http.ResponseWriter, r *http.Request) {
	bodyBytes, err := ioutil.ReadAll(r.Body)
	if err != nil {
		log.Print(err)
		return
	}

	var req commonIL.Request
	err = json.Unmarshal(bodyBytes, &req)
	if err != nil {
		log.Print(err)
		return
	}

	for _, pod := range req.Pods {
		containers := pod.Spec.Containers

		for _, container := range containers {
			delete_container(container)
		}
	}
}

func StatusHandler(w http.ResponseWriter, r *http.Request) {
	bodyBytes, err := ioutil.ReadAll(r.Body)
	if err != nil {
		log.Print(err)
		return
	}

	var req commonIL.Request
	var resp commonIL.StatusResponse
	json.Unmarshal(bodyBytes, &req)
	if err != nil {
		log.Print(err)
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

	log.Println(execReturn.Stdout)

	for _, pod := range req.Pods {
		var flag = false
		for _, jid := range JID {
			resp.PodName = append(resp.PodName, commonIL.PodName{Name: string(pod.Name)})

			cmd := []string{"-c", "squeue --me | grep " + jid.JID}
			shell := exec.ExecTask{
				Command: "bash",
				Args:    cmd,
				Shell:   true,
			}
			execReturn, _ := shell.Execute()

			if execReturn.Stdout != "" {
				flag = true
			}
		}

		if flag {
			resp.PodStatus = append(resp.PodStatus, commonIL.PodStatus{PodStatus: commonIL.RUNNING})
		} else {
			resp.PodStatus = append(resp.PodStatus, commonIL.PodStatus{PodStatus: commonIL.STOP})
		}
	}
	resp.ReturnVal = "Status"

	bodyBytes, _ = json.Marshal(resp)

	w.Write(bodyBytes)
}

func SetKubeCFGHandler(w http.ResponseWriter, r *http.Request) {
	bodyBytes, err := ioutil.ReadAll(r.Body)
	if err != nil {
		log.Fatal(err)
	}

	var req commonIL.GenericRequestType
	json.Unmarshal(bodyBytes, &req)

	os.Setenv("KUBECONFIG", req.Body)
	fmt.Println(os.Getenv("KUBECONFIG"))

	w.Write([]byte("200"))
}
