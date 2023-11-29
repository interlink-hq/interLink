package common

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/containerd/containerd/log"

	exec "github.com/alexellis/go-execute/pkg/v1"
	"gopkg.in/yaml.v2"
)

var InterLinkConfigInst InterLinkConfig
var Clientset *kubernetes.Clientset

func NewInterLinkConfig() {
	if InterLinkConfigInst.set == false {
		configPath := flag.String("configpath", "/etc/interlink/InterLinkConfig.yaml", "Path to InterLinkConfig file")
		verbose := flag.Bool("verbose", false, "Enable or disable Debug level logging")
		errorsOnly := flag.Bool("errorsonly", false, "Prints only errors if enabled")

		if *verbose {
			InterLinkConfigInst.VerboseLogging = true
			InterLinkConfigInst.ErrorsOnlyLogging = false
		} else if *errorsOnly {
			InterLinkConfigInst.VerboseLogging = false
			InterLinkConfigInst.ErrorsOnlyLogging = true
		}

		if os.Getenv("INTERLINKCONFIGPATH") != "" {
			ILcfgPath = os.Getenv("INTERLINKCONFIGPATH")
		} else {
			ILcfgPath = "/etc/interlink/InterLinkConfig.yaml"
		}
		InterLinkConfigInst.VKConfigPath = *configPath

		if _, err := os.Stat(ILcfgPath); err != nil {
			log.G(context.Background()).Error("File " + ILcfgPath + " doesn't exist. You can set a custom path by exporting INTERLINKCONFIGPATH. Exiting...")
			os.Exit(-1)
		}

		log.G(context.Background()).Info("Loading InterLink config from " + ILcfgPath)
		yfile, err := os.ReadFile(ILcfgPath)
		if err != nil {
			log.G(context.Background()).Error("Error opening config file, exiting...")
			os.Exit(1)
		}
		err = yaml.Unmarshal(yfile, &InterLinkConfigInst)
		if err != nil {
			log.G(context.Background()).Error(err)
			os.Exit(1)
		}

		if *verbose {
			InterLinkConfigInst.VerboseLogging = true
			InterLinkConfigInst.ErrorsOnlyLogging = false
		} else if *errorsOnly {
			InterLinkConfigInst.VerboseLogging = false
			InterLinkConfigInst.ErrorsOnlyLogging = true
		}

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

		if os.Getenv("POD_IP") != "" {
			InterLinkConfigInst.PodIP = os.Getenv("POD_IP")
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
			tsocksPath := os.Getenv("TSOCKSPATH")
			if _, err := os.Stat(tsocksPath); err != nil {
				log.G(context.Background()).Error("File " + tsocksPath + " doesn't exist. You can set a custom path by exporting TSOCKSPATH. Exiting...")
				os.Exit(-1)
			}

			InterLinkConfigInst.Tsockspath = tsocksPath
		}

		var tokenPath string
		if os.Getenv("VKTOKENFILE") != "" {
			tokenPath = os.Getenv("VKTOKENFILE")
			if _, err := os.Stat(tokenPath); err != nil {
				log.G(context.Background()).Error("File " + tokenPath + " doesn't exist. You can set a custom path by exporting VKTOKENFILE. Exiting...")
				os.Exit(-1)
			}

			InterLinkConfigInst.VKTokenFile = tokenPath
		} else {
			tokenPath = InterLinkConfigInst.DataRootFolder + "token"
			InterLinkConfigInst.VKTokenFile = tokenPath
		}

		InterLinkConfigInst.set = true
	}
}

func NewServiceAccount() error {

	var sa string
	var script string
	path := InterLinkConfigInst.DataRootFolder + ".kube/"

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

	script = "#!" + InterLinkConfigInst.BashPath + "\n" +
		"SERVICE_ACCOUNT_NAME=" + InterLinkConfigInst.ServiceAccount + "\n" +
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

	//executing the script to actually retrieve a valid service account
	cmd := []string{path + "getSAConfig.sh"}
	shell := exec.ExecTask{
		Command: "sh",
		Args:    cmd,
		Shell:   true,
	}
	execResult, _ := shell.Execute()
	if execResult.Stderr != "" {
		log.G(context.Background()).Error("Stderr: " + execResult.Stderr + "\nStdout: " + execResult.Stdout)
		return errors.New(execResult.Stderr)
	}

	//checking if the config is valid
	_, err = clientcmd.LoadFromFile(path + "kubeconfig-sa")
	if err != nil {
		log.G(context.Background()).Error(err)
		return err
	}

	config, err := os.ReadFile(path + "kubeconfig-sa")
	if err != nil {
		log.G(context.Background()).Error(err)
		return err
	}

	sa = string(config)
	os.Remove(path + "getSAConfig.sh")
	os.Remove(path + "kubeconfig-sa")

	for {
		var returnValue, _ = json.Marshal("Error")
		reader := bytes.NewReader([]byte(sa))
		req, err := http.NewRequest(http.MethodPost, InterLinkConfigInst.Interlinkurl+":"+InterLinkConfigInst.Interlinkport+"/setKubeCFG", reader)

		if err != nil {
			log.G(context.Background()).Error(err)
		}

		token, err := os.ReadFile(InterLinkConfigInst.VKTokenFile) // just pass the file name
		if err != nil {
			log.G(context.Background()).Error(err)
			return err
		}
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

func PingInterLink() (error, bool) {
	req, err := http.NewRequest(http.MethodPost, InterLinkConfigInst.Interlinkurl+":"+InterLinkConfigInst.Interlinkport+"/ping", nil)

	if err != nil {
		log.G(context.Background()).Error(err)
	}

	token, err := os.ReadFile(InterLinkConfigInst.VKTokenFile) // just pass the file name
	if err != nil {
		log.G(context.Background()).Error(err)
		return err, false
	}
	req.Header.Add("Authorization", "Bearer "+string(token))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err, false
	}

	if resp.StatusCode == http.StatusOK {
		return nil, true
	} else {
		log.G(context.Background()).Error("Error " + err.Error() + " " + fmt.Sprint(resp.StatusCode))
		return nil, false
	}
}
