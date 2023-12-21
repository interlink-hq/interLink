package virtualkubelet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	exec "github.com/alexellis/go-execute/pkg/v1"
	commonIL "github.com/intertwin-eu/interlink/pkg/common"

	"github.com/containerd/containerd/log"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

var ClientSet *kubernetes.Clientset

func NewServiceAccount() error {

	var sa string
	var script string
	path := commonIL.InterLinkConfigInst.DataRootFolder + ".kube/"

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

	script = "#!" + commonIL.InterLinkConfigInst.BashPath + "\n" +
		"SERVICE_ACCOUNT_NAME=" + commonIL.InterLinkConfigInst.ServiceAccount + "\n" +
		"CONTEXT=$(kubectl config current-context)\n" +
		"NAMESPACE=" + commonIL.InterLinkConfigInst.Namespace + "\n" +
		"NEW_CONTEXT=" + commonIL.InterLinkConfigInst.Namespace + "\n" +
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

	err = commonIL.CreateClientsetFrom(context.Background(), sa)

	return nil
}

func createRequest(pods []*v1.Pod, token string) ([]byte, error) {
	var returnValue, _ = json.Marshal(commonIL.PodStatus{})

	bodyBytes, err := json.Marshal(pods)
	if err != nil {
		log.L.Error(err)
		return nil, err
	}
	reader := bytes.NewReader(bodyBytes)
	req, err := http.NewRequest(http.MethodPost, commonIL.InterLinkConfigInst.Interlinkurl+":"+commonIL.InterLinkConfigInst.Interlinkport+"/create", reader)
	if err != nil {
		log.L.Error(err)
		return nil, err
	}

	req.Header.Add("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.L.Error(err)
		return nil, err
	}
	statusCode := resp.StatusCode

	if statusCode != http.StatusOK {
		return nil, errors.New("Unexpected error occured while creating Pods. Status code: " + strconv.Itoa(resp.StatusCode) + ". Check InterLink's logs for further informations")
	} else {
		log.G(context.Background()).Info(string(returnValue))
		returnValue, err = io.ReadAll(resp.Body)
		if err != nil {
			log.L.Error(err)
			return nil, err
		}
	}

	return returnValue, nil
}

func deleteRequest(pods []*v1.Pod, token string) ([]byte, error) {
	returnValue, _ := json.Marshal(commonIL.PodStatus{})

	bodyBytes, err := json.Marshal(pods)
	if err != nil {
		log.G(context.Background()).Error(err)
		return nil, err
	}
	reader := bytes.NewReader(bodyBytes)
	req, err := http.NewRequest(http.MethodDelete, commonIL.InterLinkConfigInst.Interlinkurl+":"+commonIL.InterLinkConfigInst.Interlinkport+"/delete", reader)
	if err != nil {
		log.G(context.Background()).Error(err)
		return nil, err
	}

	req.Header.Add("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.G(context.Background()).Error(err)
		return nil, err
	}

	statusCode := resp.StatusCode

	if statusCode != http.StatusOK {
		return nil, errors.New("Unexpected error occured while deleting Pods. Status code: " + strconv.Itoa(resp.StatusCode) + ". Check InterLink's logs for further informations")
	} else {
		returnValue, _ = io.ReadAll(resp.Body)
		log.G(context.Background()).Info(string(returnValue))
		var response []commonIL.PodStatus
		err = json.Unmarshal(returnValue, &response)
		if err != nil {
			log.G(context.Background()).Error(err)
			return nil, err
		}
	}

	return returnValue, nil
}

func statusRequest(podsList []*v1.Pod, token string) ([]byte, error) {
	var returnValue []byte

	bodyBytes, err := json.Marshal(podsList)
	if err != nil {
		log.L.Error(err)
		return nil, err
	}
	reader := bytes.NewReader(bodyBytes)
	req, err := http.NewRequest(http.MethodGet, commonIL.InterLinkConfigInst.Interlinkurl+":"+commonIL.InterLinkConfigInst.Interlinkport+"/status", reader)
	if err != nil {
		log.L.Error(err)
		return nil, err
	}

	//log.L.Println(string(bodyBytes))

	req.Header.Add("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("Unexpected error occured while getting status. Status code: " + strconv.Itoa(resp.StatusCode) + ". Check InterLink's logs for further informations")
	} else {
		returnValue, _ = io.ReadAll(resp.Body)
		if err != nil {
			log.L.Error(err)
			return nil, err
		}
	}

	return returnValue, nil
}

func LogRetrieval(p *VirtualKubeletProvider, ctx context.Context, logsRequest commonIL.LogStruct) (io.ReadCloser, error) {
	b, err := os.ReadFile(commonIL.InterLinkConfigInst.VKTokenFile) // just pass the file name
	if err != nil {
		log.G(ctx).Fatal(err)
	}
	token := string(b)

	bodyBytes, err := json.Marshal(logsRequest)
	if err != nil {
		log.G(ctx).Error(err)
		return nil, err
	}
	reader := bytes.NewReader(bodyBytes)
	req, err := http.NewRequest(http.MethodGet, commonIL.InterLinkConfigInst.Interlinkurl+":"+commonIL.InterLinkConfigInst.Interlinkport+"/getLogs", reader)
	if err != nil {
		log.G(ctx).Error(err)
		return nil, err
	}

	log.G(ctx).Println(string(bodyBytes))

	req.Header.Add("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.G(ctx).Error(err)
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		log.G(ctx).Info(resp.Body)
		return nil, errors.New("Unexpected error occured while getting logs. Status code: " + strconv.Itoa(resp.StatusCode) + ". Check InterLink's logs for further informations")
	} else {
		return resp.Body, nil
	}
}

func RemoteExecution(p *VirtualKubeletProvider, ctx context.Context, mode int8, imageLocation string, pod *v1.Pod) error {
	var req []*v1.Pod
	req = []*v1.Pod{pod}

	b, err := os.ReadFile(commonIL.InterLinkConfigInst.VKTokenFile) // just pass the file name
	if err != nil {
		log.G(ctx).Fatal(err)
	}
	token := string(b)

	switch mode {
	case CREATE:

		for {
			var err error
			if ClientSet == nil {
				kubeconfig := os.Getenv("KUBECONFIG")
				if err != nil {
					log.G(ctx).Error(err)
					return err
				}

				config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
				if err != nil {
					log.G(ctx).Error(err)
					return err
				}

				ClientSet, err = kubernetes.NewForConfig(config)
				if err != nil {
					log.G(ctx).Error(err)
					return err
				}
			}

			for _, volume := range pod.Spec.Volumes {

				if volume.ConfigMap != nil {
					_, err = ClientSet.CoreV1().ConfigMaps(pod.Namespace).Get(ctx, volume.ConfigMap.Name, metav1.GetOptions{})
					if err != nil {
						log.G(ctx).Warning("Unable to find ConfigMap " + volume.ConfigMap.Name + " for pod " + pod.Name + ". Waiting for it to be initialized")
					}
				} else if volume.Secret != nil {
					_, err = ClientSet.CoreV1().Secrets(pod.Namespace).Get(ctx, volume.Secret.SecretName, metav1.GetOptions{})
					if err != nil {
						log.G(ctx).Warning("Unable to find Secret " + volume.Secret.SecretName + " for pod " + pod.Name + ". Waiting for it to be initialized")
					}
				}
			}

			if err != nil {
				time.Sleep(time.Second)
				continue
			} else {
				break
			}
		}

		returnVal, err := createRequest(req, token)
		if err != nil {
			log.G(ctx).Error(err)
			return err
		}
		log.G(ctx).Info(string(returnVal))
		break
	case DELETE:
		returnVal, err := deleteRequest(req, token)
		if err != nil {
			log.G(ctx).Error(err)
			return err
		}
		log.G(ctx).Info(string(returnVal))
	}
	return nil
}

func checkPodsStatus(p *VirtualKubeletProvider, ctx context.Context, token string) error {
	if len(p.pods) == 0 {
		return nil
	}
	var returnVal []byte
	var ret []commonIL.PodStatus
	var PodsList []*v1.Pod

	for _, pod := range p.pods {
		PodsList = append(PodsList, pod)
	}
	//log.G(ctx).Debug(p.pods) //commented out because it's too verbose. uncomment to see all registered pods

	returnVal, err := statusRequest(PodsList, token)
	if err != nil {
		return err
	} else if returnVal != nil {
		err = json.Unmarshal(returnVal, &ret)
		if err != nil {
			return err
		}

		for _, podStatus := range ret {
			updatePod := false

			pod, err := p.GetPod(ctx, podStatus.PodNamespace, podStatus.PodName)
			if err != nil {
				log.G(ctx).Error(err)
				return err
			}

			if podStatus.PodUID == string(pod.UID) {
				for _, containerStatus := range podStatus.Containers {
					index := 0

					for i, checkedContainer := range pod.Status.ContainerStatuses {
						if checkedContainer.Name == containerStatus.Name {
							index = i
						}
					}

					if containerStatus.State.Terminated != nil {
						log.G(ctx).Info("Pod " + podStatus.PodName + ": Service " + containerStatus.Name + " is not running on Sidecar")
						updatePod = false
						if containerStatus.State.Terminated.ExitCode == 0 {
							pod.Status.Phase = v1.PodSucceeded
							updatePod = true
						} else {
							pod.Status.Phase = v1.PodFailed
							updatePod = true
							log.G(ctx).Error("Container " + containerStatus.Name + " exited with error: " + string(containerStatus.State.Terminated.ExitCode))
						}
					} else if containerStatus.State.Waiting != nil {
						log.G(ctx).Info("Pod " + podStatus.PodName + ": Service " + containerStatus.Name + " is setting up on Sidecar")
						updatePod = false
					} else if containerStatus.State.Running != nil {
						pod.Status.Phase = v1.PodRunning
						updatePod = true
						if pod.Status.ContainerStatuses != nil {
							pod.Status.ContainerStatuses[index].State = containerStatus.State
							pod.Status.ContainerStatuses[index].Ready = containerStatus.Ready
						}
					}
				}
			}

			if updatePod {
				err = p.UpdatePod(ctx, pod)
				if err != nil {
					log.G(ctx).Error(err)
					return err
				}
			}
		}

		log.G(ctx).Info("No errors while getting statuses")
		log.G(ctx).Debug(ret)
		return nil
	}
	return err
}
