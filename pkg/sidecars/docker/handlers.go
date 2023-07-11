package docker

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
)

func StatusHandler(w http.ResponseWriter, r *http.Request) {
	//call to docker get status
	var resp commonIL.StatusResponse

	bodyBytes, err := ioutil.ReadAll(r.Body)
	if err != nil {
		log.Fatal(err)
	}

	var req commonIL.Request
	json.Unmarshal(bodyBytes, &req)

	for _, pod := range req.Pods {
		for _, container := range pod.Spec.Containers {
			cmd := []string{"ps -aqf \"name= " + container.Name + "\""}

			shell := exec.ExecTask{
				Command: "docker",
				Args:    cmd,
				Shell:   true,
			}
			execReturn, err := shell.Execute()
			execReturn.Stdout = strings.ReplaceAll(execReturn.Stdout, "\n", "")

			if err != nil {
				log.Fatal(err)
			}

			resp.PodName = append(resp.PodName, commonIL.PodName{Name: pod.Name})
			log.Println(execReturn.Stderr, execReturn.Stdout)

			if execReturn.Stdout == "" {
				resp.PodStatus = append(resp.PodStatus, commonIL.PodStatus{PodStatus: commonIL.STOP})
			} else {
				resp.PodStatus = append(resp.PodStatus, commonIL.PodStatus{PodStatus: commonIL.RUNNING})
			}
		}
	}

	resp.ReturnVal = "Status"
	bodyBytes, _ = json.Marshal(resp)

	w.Write(bodyBytes)
}

func CreateHandler(w http.ResponseWriter, r *http.Request) {
	var execReturn exec.ExecResult
	bodyBytes, err := ioutil.ReadAll(r.Body)
	if err != nil {
		log.Fatal(err)
	}

	var req commonIL.Request
	json.Unmarshal(bodyBytes, &req)

	for _, pod := range req.Pods {
		for _, container := range pod.Spec.Containers {
			cmd := []string{"run", "-d", "--name", container.Name}

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
				log.Fatal(err)
			}

			shell = exec.ExecTask{
				Command: "docker",
				Args:    []string{"ps", "-aqf", "name=^" + container.Name + "$"},
				Shell:   true,
			}

			execReturn, err = shell.Execute()
			execReturn.Stdout = strings.ReplaceAll(execReturn.Stdout, "\n", "")
		}
	}
	if err != nil {
		w.Write([]byte(execReturn.Stderr))
	} else {
		w.Write([]byte("All containers for submitted Pods have been created"))
	}
}

func DeleteHandler(w http.ResponseWriter, r *http.Request) {
	var execReturn exec.ExecResult
	bodyBytes, err := ioutil.ReadAll(r.Body)
	if err != nil {
		log.Fatal(err)
	}

	var req commonIL.Request
	json.Unmarshal(bodyBytes, &req)

	for _, pod := range req.Pods {
		cmd := []string{"stop", pod.Name}
		shell := exec.ExecTask{
			Command: "docker",
			Args:    cmd,
			Shell:   true,
		}
		execReturn, err = shell.Execute()

		cmd = []string{"rm", execReturn.Stdout}
		shell = exec.ExecTask{
			Command: "docker",
			Args:    cmd,
			Shell:   true,
		}
		execReturn, err = shell.Execute()
		execReturn.Stdout = strings.ReplaceAll(execReturn.Stdout, "\n", "")

		if err != nil {
			log.Fatal(err)
		}
	}

	if err != nil {
		w.Write([]byte(execReturn.Stderr))
	} else {
		w.Write([]byte("All containers for submitted Pods have been deleted"))
	}
}

func SetKubeCFGHandler(w http.ResponseWriter, r *http.Request) {
	path := ".kube/"
	bodyBytes, err := ioutil.ReadAll(r.Body)
	if err != nil {
		log.Fatal(err)
	}

	var req commonIL.GenericRequestType
	json.Unmarshal(bodyBytes, &req)

	err = os.MkdirAll(path, os.ModePerm)
	if err != nil {
		log.Println(err)
	}
	config, err := os.Create(path + "config")
	if err != nil {
		log.Println(err)
	}
	_, err = config.Write([]byte(req.Body))
	if err != nil {
		log.Println(err)
	}
	defer config.Close()
	os.Setenv("KUBECONFIG", path+"config")
	fmt.Println(os.Getenv("KUBECONFIG"))

	w.Write([]byte("200"))
}
