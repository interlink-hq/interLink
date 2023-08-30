package common

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/containerd/containerd/log"

	exec "github.com/alexellis/go-execute/pkg/v1"
	"gopkg.in/yaml.v2"
)

var InterLinkConfigInst InterLinkConfig

func NewInterLinkConfig() {
	if InterLinkConfigInst.set == false {
		var path string
		if os.Getenv("INTERLINKCONFIGPATH") != "" {
			path = os.Getenv("INTERLINKCONFIGPATH")
		} else {
			path = "/etc/interlink/InterLinkConfig.yaml"
		}

		if _, err := os.Stat(path); err != nil {
			log.G(context.Background()).Error("File " + path + " doesn't exist. You can set a custom path by exporting INTERLINKCONFIGPATH. Exiting...")
			os.Exit(-1)
		}

		yfile, err := os.ReadFile(path)
		if err != nil {
			log.G(context.Background()).Error("Error opening config file, exiting...")
			os.Exit(1)
		}
		yaml.Unmarshal(yfile, &InterLinkConfigInst)

		if os.Getenv("INTERLINKURL") != "" {
			InterLinkConfigInst.Interlinkurl = os.Getenv("INTERLINKURL")
		}

		if os.Getenv("SIDECARURL") != "" {
			InterLinkConfigInst.Sidecarurl = os.Getenv("SIDECARURL")
		}

		if os.Getenv("INTERLINKPORT") != "" {
			InterLinkConfigInst.Interlinkport = os.Getenv("INTERLINKPORT")
		}

		if os.Getenv("SIDECARPORT") != "" {
			InterLinkConfigInst.Sidecarport = os.Getenv("SIDECARPORT")
		} else {
		}

		if os.Getenv("SBATCHPATH") != "" {
			InterLinkConfigInst.Sbatchpath = os.Getenv("SBATCHPATH")
		}

		if os.Getenv("SCANCELPATH") != "" {
			InterLinkConfigInst.Scancelpath = os.Getenv("SCANCELPATH")
		}

		if os.Getenv("TSOCKS") != "" {
			if os.Getenv("TSOCKS") != "true" && os.Getenv("TSOCKS") != "false" {
				fmt.Println("export TSOCKS as true or false")
				os.Exit(-1)
			}
			if os.Getenv("TSOCKS") == "true" {
				InterLinkConfigInst.Tsocks = true
			} else {
				InterLinkConfigInst.Tsocks = false
			}
		}

		if os.Getenv("TSOCKSPATH") != "" {
			path := os.Getenv("TSOCKSPATH")
			if _, err := os.Stat(path); err != nil {
				log.G(context.Background()).Error("File " + path + " doesn't exist. You can set a custom path by exporting TSOCKSPATH. Exiting...")
				os.Exit(-1)
			}

			InterLinkConfigInst.Tsockspath = path
		}

		if os.Getenv("VKTOKENFILE") != "" {
			path := os.Getenv("VKTOKENFILE")
			if _, err := os.Stat(path); err != nil {
				log.G(context.Background()).Error("File " + path + " doesn't exist. You can set a custom path by exporting VKTOKENFILE. Exiting...")
				os.Exit(-1)
			}

			InterLinkConfigInst.VKTokenFile = path
		} else {
			path = "/tmp/token"
			InterLinkConfigInst.VKTokenFile = path
		}

		InterLinkConfigInst.set = true
	}
}

func NewServiceAccount() error {

	var sa string
	var script string
	path := "/tmp/"

	err := os.MkdirAll(path, os.ModePerm)
	if err != nil {
		log.G(context.Background()).Error(err)
		return err
	}
	f, err := os.Create(path + "getSAConfig.sh")
	if err != nil {
		log.G(context.Background()).Error(err)
		return err
	}

	defer f.Close()

	script = "SERVICE_ACCOUNT_NAME=" + InterLinkConfigInst.ServiceAccount + "\n" +
		"CONTEXT=$(kubectl config current-context)\n" +
		"NAMESPACE=" + InterLinkConfigInst.Namespace + "\n" +
		"NEW_CONTEXT=" + InterLinkConfigInst.Namespace + "\n" +
		"KUBECONFIG_FILE=\"" + path + "kubeconfig-sa\"\n" +
		"SECRET_NAME=$(kubectl get secret -l kubernetes.io/service-account.name=${SERVICE_ACCOUNT_NAME} --namespace ${NAMESPACE} --context ${CONTEXT} -o jsonpath='{.items[0].metadata.name}')\n" +
		"TOKEN_DATA=$(kubectl get secret ${SECRET_NAME} --context ${CONTEXT} --namespace ${NAMESPACE} -o jsonpath='{.data.token}')\n" +
		"TOKEN=$(echo ${TOKEN_DATA} | base64 -d)\n" +
		"kubectl config view --raw > ${KUBECONFIG_FILE}.full.tmp\n" +
		"kubectl --kubeconfig ${KUBECONFIG_FILE}.full.tmp config use-context ${CONTEXT}\n" +
		"kubectl --kubeconfig ${KUBECONFIG_FILE}.full.tmp config view --flatten --minify > ${KUBECONFIG_FILE}.tmp\n" +
		"kubectl config --kubeconfig ${KUBECONFIG_FILE}.tmp rename-context ${CONTEXT} ${NEW_CONTEXT}\n" +
		"kubectl config --kubeconfig ${KUBECONFIG_FILE}.tmp set-credentials ${CONTEXT}-${NAMESPACE}-token-user --token ${TOKEN}\n" +
		"kubectl config --kubeconfig ${KUBECONFIG_FILE}.tmp set-context ${NEW_CONTEXT} --user ${CONTEXT}-${NAMESPACE}-token-user\n" +
		"kubectl config --kubeconfig ${KUBECONFIG_FILE}.tmp set-context ${NEW_CONTEXT} --namespace ${NAMESPACE}\n" +
		"kubectl config --kubeconfig ${KUBECONFIG_FILE}.tmp view --flatten --minify > ${KUBECONFIG_FILE}\n" +
		"rm ${KUBECONFIG_FILE}.full.tmp\n" +
		"rm ${KUBECONFIG_FILE}.tmp"

	_, err = f.Write([]byte(script))

	if err != nil {
		log.G(context.Background()).Error(err)
		return err
	}

	cmd := []string{path + "getSAConfig.sh"}
	shell := exec.ExecTask{
		Command: "sh",
		Args:    cmd,
		Shell:   true,
	}
	execResult, _ := shell.Execute()
	if execResult.Stderr != "" {
		log.G(context.Background()).Error(execResult.Stderr)
		return errors.New(execResult.Stderr)
	}

	temp, err := os.ReadFile(path + "kubeconfig-sa")
	if err != nil {
		log.G(context.Background()).Error(err)
		return err
	}

	sa = string(temp)
	os.Remove(path + "getSAConfig.sh")
	os.Remove(path + "kubeconfig-sa")

	for {

		var returnValue, _ = json.Marshal("Error")
		//request := GenericRequestType{Body: sa}

		//bodyBytes, err := json.Marshal(request)
		reader := bytes.NewReader([]byte(sa))
		req, err := http.NewRequest(http.MethodPost, InterLinkConfigInst.Interlinkurl+":"+InterLinkConfigInst.Interlinkport+"/setKubeCFG", reader)

		if err != nil {
			log.G(context.Background()).Error(err)
		}

		token, err := os.ReadFile(InterLinkConfigInst.VKTokenFile) // just pass the file name
		req.Header.Add("Authorization", "Bearer "+string(token))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			log.G(context.Background()).Error(err)
			time.Sleep(5 * time.Second)
			continue
		} else {

			returnValue, _ = io.ReadAll(resp.Body)
		}

		if resp.StatusCode == http.StatusOK {
			break
		} else {
			log.G(context.Background()).Error("Error " + err.Error() + " " + string(returnValue))
		}
	}

	return nil
}
