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

	commonIL "github.com/intertwin-eu/interlink/pkg/common"

	"github.com/containerd/containerd/log"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

var ClientSet *kubernetes.Clientset

func updateCacheRequest(uid string, token string, config commonIL.InterLinkConfig) error {
	bodyBytes := []byte(uid)

	reader := bytes.NewReader(bodyBytes)
	req, err := http.NewRequest(http.MethodPost, config.Interlinkurl+":"+config.Interlinkport+"/updateCache", reader)
	if err != nil {
		log.L.Error(err)
		return err
	}

	req.Header.Add("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.L.Error(err)
		return err
	}
	statusCode := resp.StatusCode

	if statusCode != http.StatusOK {
		return errors.New("Unexpected error occured while updating InterLink cache. Status code: " + strconv.Itoa(resp.StatusCode) + ". Check InterLink's logs for further informations")
	}

	return err
}

func createRequest(pod commonIL.PodCreateRequests, token string, config commonIL.InterLinkConfig) ([]byte, error) {
	var returnValue, _ = json.Marshal(commonIL.PodStatus{})

	bodyBytes, err := json.Marshal(pod)
	if err != nil {
		log.L.Error(err)
		return nil, err
	}
	reader := bytes.NewReader(bodyBytes)
	req, err := http.NewRequest(http.MethodPost, config.Interlinkurl+":"+config.Interlinkport+"/create", reader)
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
		returnValue, err = io.ReadAll(resp.Body)
		if err != nil {
			log.L.Error(err)
			return nil, err
		}
		log.G(context.Background()).Info(string(returnValue))
	}

	return returnValue, nil
}

func deleteRequest(pod *v1.Pod, token string, config commonIL.InterLinkConfig) ([]byte, error) {
	bodyBytes, err := json.Marshal(pod)
	if err != nil {
		log.G(context.Background()).Error(err)
		return nil, err
	}
	reader := bytes.NewReader(bodyBytes)
	req, err := http.NewRequest(http.MethodDelete, config.Interlinkurl+":"+config.Interlinkport+"/delete", reader)
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
		returnValue, err := io.ReadAll(resp.Body)
		if err != nil {
			log.G(context.Background()).Error(err)
			return nil, err
		}
		log.G(context.Background()).Info(string(returnValue))
		var response []commonIL.PodStatus
		err = json.Unmarshal(returnValue, &response)
		if err != nil {
			log.G(context.Background()).Error(err)
			return nil, err
		}
		return returnValue, nil
	}

}

func statusRequest(podsList []*v1.Pod, token string, config commonIL.InterLinkConfig) ([]byte, error) {
	var returnValue []byte

	bodyBytes, err := json.Marshal(podsList)
	if err != nil {
		log.L.Error(err)
		return nil, err
	}
	reader := bytes.NewReader(bodyBytes)
	req, err := http.NewRequest(http.MethodGet, config.Interlinkurl+":"+config.Interlinkport+"/status", reader)
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
		returnValue, err = io.ReadAll(resp.Body)
		if err != nil {
			log.L.Error(err)
			return nil, err
		}
	}

	return returnValue, nil
}

func LogRetrieval(ctx context.Context, logsRequest commonIL.LogStruct, config commonIL.InterLinkConfig) (io.ReadCloser, error) {
	b, err := os.ReadFile(config.VKTokenFile) // just pass the file name
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
	req, err := http.NewRequest(http.MethodGet, config.Interlinkurl+":"+config.Interlinkport+"/getLogs", reader)
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

func RemoteExecution(p *VirtualKubeletProvider, ctx context.Context, mode int8, pod *v1.Pod, config commonIL.InterLinkConfig) error {

	b, err := os.ReadFile(config.VKTokenFile) // just pass the file name
	if err != nil {
		log.G(ctx).Fatal(err)
		return err
	}
	token := string(b)

	switch mode {
	case CREATE:
		var req commonIL.PodCreateRequests
		req.Pod = *pod
		startTime := time.Now()

		for {
			timeNow := time.Now()
			if timeNow.Sub(startTime).Seconds() < time.Hour.Minutes()*5 {
				if ClientSet == nil {
					kubeconfig := os.Getenv("KUBECONFIG")

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

				pod, err = ClientSet.CoreV1().Pods(pod.Namespace).Get(ctx, pod.Name, metav1.GetOptions{})
				if err != nil {
					return errors.New("Deleted pod before actual creation")
				}

				var failed bool

				for _, volume := range pod.Spec.Volumes {

					if volume.ConfigMap != nil {
						cfgmap, err := ClientSet.CoreV1().ConfigMaps(pod.Namespace).Get(ctx, volume.ConfigMap.Name, metav1.GetOptions{})
						if err != nil {
							failed = true
							log.G(ctx).Warning("Unable to find ConfigMap " + volume.ConfigMap.Name + " for pod " + pod.Name + ". Waiting for it to be initialized")
							if pod.Status.Phase != "Initializing" {
								pod.Status.Phase = "Initializing"
								p.UpdatePod(ctx, pod)
							}
							break
						} else {
							req.ConfigMaps = append(req.ConfigMaps, *cfgmap)
						}
					} else if volume.Secret != nil {
						scrt, err := ClientSet.CoreV1().Secrets(pod.Namespace).Get(ctx, volume.Secret.SecretName, metav1.GetOptions{})
						if err != nil {
							failed = true
							log.G(ctx).Warning("Unable to find Secret " + volume.Secret.SecretName + " for pod " + pod.Name + ". Waiting for it to be initialized")
							if pod.Status.Phase != "Initializing" {
								pod.Status.Phase = "Initializing"
								p.UpdatePod(ctx, pod)
							}
							break
						} else {
							req.Secrets = append(req.Secrets, *scrt)
						}
					}
				}

				if failed {
					time.Sleep(time.Second)
					continue
				} else {
					pod.Status.Phase = v1.PodPending
					p.UpdatePod(ctx, pod)
					break
				}
			} else {
				pod.Status.Phase = v1.PodFailed
				pod.Status.Reason = "CFGMaps/Secrets not found"
				for _, ct := range pod.Status.ContainerStatuses {
					ct.Ready = false
				}
				p.UpdatePod(ctx, pod)
				return errors.New("Unable to retrieve ConfigMaps or Secrets. Check logs.")
			}
		}

		returnVal, err := createRequest(req, token, config)
		if err != nil {
			log.G(ctx).Error(err)
			return err
		}
		log.G(ctx).Info(string(returnVal))

	case DELETE:
		req := pod
		if pod.Status.Phase != "Initializing" {
			returnVal, err := deleteRequest(req, token, config)
			if err != nil {
				log.G(ctx).Error(err)
				return err
			}
			log.G(ctx).Info(string(returnVal))
		}
	}
	return nil
}

func checkPodsStatus(p *VirtualKubeletProvider, ctx context.Context, token string, config commonIL.InterLinkConfig) error {
	if len(p.pods) == 0 {
		return nil
	}
	var returnVal []byte
	var ret []commonIL.PodStatus
	var PodsList []*v1.Pod
	var err error

	for _, pod := range p.pods {
		if pod.Status.Phase == v1.PodPending || pod.Status.Phase == v1.PodRunning {
			PodsList = append(PodsList, pod)
		}
	}
	//log.G(ctx).Debug(p.pods) //commented out because it's too verbose. uncomment to see all registered pods

	if PodsList != nil {
		returnVal, err = statusRequest(PodsList, token, config)
		if err != nil {
			return err
		} else if returnVal != nil {
			err = json.Unmarshal(returnVal, &ret)
			if err != nil {
				return err
			}

			for _, podStatus := range ret {

				pod, err := p.GetPod(ctx, podStatus.PodNamespace, podStatus.PodName)
				if err != nil {
					updateCacheRequest(podStatus.PodUID, token, config)
					log.G(ctx).Warning("Error: " + err.Error() + "while getting statuses. Updating InterLink cache")
					return err
				}

				if podStatus.PodUID == string(pod.UID) {
					podRunning := false
					podErrored := false
					failedReason := ""
					for _, containerStatus := range podStatus.Containers {
						index := 0
						foundCt := false

						for i, checkedContainer := range pod.Status.ContainerStatuses {
							if checkedContainer.Name == containerStatus.Name {
								foundCt = true
								index = i
							}
						}

						if !foundCt {
							pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, containerStatus)
						} else {
							pod.Status.ContainerStatuses[index] = containerStatus
						}

						if containerStatus.State.Terminated != nil {
							log.G(ctx).Debug("Pod " + podStatus.PodName + ": Service " + containerStatus.Name + " is not running on Sidecar")
							pod.Status.ContainerStatuses[index].State.Terminated.Reason = "Completed"
							if containerStatus.State.Terminated.ExitCode != 0 {
								podErrored = true
								failedReason = "Error: " + string(containerStatus.State.Terminated.ExitCode)
								pod.Status.ContainerStatuses[index].State.Terminated.Reason = failedReason
								log.G(ctx).Error("Container " + containerStatus.Name + " exited with error: " + string(containerStatus.State.Terminated.ExitCode))
							}
						} else if containerStatus.State.Waiting != nil {
							log.G(ctx).Info("Pod " + podStatus.PodName + ": Service " + containerStatus.Name + " is setting up on Sidecar")
							podRunning = true
						} else if containerStatus.State.Running != nil {
							podRunning = true
							log.G(ctx).Debug("Pod " + podStatus.PodName + ": Service " + containerStatus.Name + " is running on Sidecar")
						}

					}

					if podRunning {
						if pod.Status.Phase != v1.PodRunning {
							pod.Status.Phase = v1.PodRunning
						}
					} else {
						if podErrored {
							if pod.Status.Phase != v1.PodFailed {
								pod.Status.Phase = v1.PodFailed
								pod.Status.Reason = failedReason
							}
						} else {
							if pod.Status.Phase != v1.PodSucceeded {
								pod.Status.Phase = v1.PodSucceeded
								pod.Status.Reason = "Completed"
							}
						}
					}

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
	}
	return err
}
