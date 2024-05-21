package virtualkubelet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/containerd/containerd/log"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	commonIL "github.com/intertwin-eu/interlink/pkg/interlink"
)

// PingInterLink pings the InterLink API and returns true if there's an answer. The second return value is given by the answer provided by the API.
func PingInterLink(ctx context.Context, config VirtualKubeletConfig) (bool, int, error) {
	log.G(ctx).Info("Pinging: " + config.Interlinkurl + ":" + config.Interlinkport + "/pinglink")
	retVal := -1
	req, err := http.NewRequest(http.MethodPost, config.Interlinkurl+":"+config.Interlinkport+"/pinglink", nil)

	if err != nil {
		log.G(ctx).Error(err)
	}

	token, err := os.ReadFile(config.VKTokenFile) // just pass the file name
	if err != nil {
		log.G(ctx).Error(err)
		return false, retVal, err
	}
	req.Header.Add("Authorization", "Bearer "+string(token))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, retVal, err
	}

	if resp.StatusCode == http.StatusOK {
		retBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			log.G(ctx).Error(err)
			return false, retVal, err
		}
		retVal, err = strconv.Atoi(string(retBytes))
		if err != nil {
			log.G(ctx).Error(err)
			return false, retVal, err
		}
		return true, retVal, nil
	} else {
		log.G(ctx).Error("server error: " + fmt.Sprint(resp.StatusCode))
		return false, retVal, nil
	}
}

// updateCacheRequest is called when the VK receives the status of a pod already deleted. It performs a REST call InterLink API to update the cache deleting that pod from the cached structure
func updateCacheRequest(config VirtualKubeletConfig, pod v1.Pod, token string) error {
	bodyBytes, err := json.Marshal(pod)
	if err != nil {
		log.L.Error(err)
		return err
	}

	reader := bytes.NewReader(bodyBytes)
	req, err := http.NewRequest(http.MethodPost, config.Interlinkurl+":"+config.Interlinkport+"/updateCache", reader)
	if err != nil {
		log.L.Error(err)
		return err
	}

	req.Header.Add("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
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

// createRequest performs a REST call to the InterLink API when a Pod is registered to the VK. It Marshals the pod with already retrieved ConfigMaps and Secrets and sends it to InterLink.
// Returns the call response expressed in bytes and/or the first encountered error
func createRequest(config VirtualKubeletConfig, pod commonIL.PodCreateRequests, token string) ([]byte, error) {
	var returnValue, _ = json.Marshal(commonIL.CreateStruct{})

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
	req.Header.Set("Content-Type", "application/json")
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
	}

	return returnValue, nil
}

// deleteRequest performs a REST call to the InterLink API when a Pod is deleted from the VK. It Marshals the standard v1.Pod struct and sends it to InterLink.
// Returns the call response expressed in bytes and/or the first encountered error
func deleteRequest(config VirtualKubeletConfig, pod *v1.Pod, token string) ([]byte, error) {
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
	req.Header.Set("Content-Type", "application/json")
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

// statusRequest performs a REST call to the InterLink API when the VK needs an update on its Pods' status. A Marshalled slice of v1.Pod is sent to the InterLink API,
// to query the below plugin for their status.
// Returns the call response expressed in bytes and/or the first encountered error
func statusRequest(config VirtualKubeletConfig, podsList []*v1.Pod, token string) ([]byte, error) {
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

	req.Header.Set("Content-Type", "application/json")
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

// LogRetrieval performs a REST call to the InterLink API when the user ask for a log retrieval. Compared to create/delete/status request, a way smaller struct is marshalled and sent.
// This struct only includes a minimum data set needed to identify the job/container to get the logs from.
// Returns the call response and/or the first encountered error
func LogRetrieval(ctx context.Context, config VirtualKubeletConfig, logsRequest commonIL.LogStruct) (io.ReadCloser, error) {
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

	req.Header.Set("Content-Type", "application/json")
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

// RemoteExecution is called by the VK everytime a Pod is being registered or deleted to/from the VK.
// Depending on the mode (CREATE/DELETE), it performs different actions, making different REST calls.
// Note: for the CREATE mode, the function gets stuck up to 5 minutes waiting for every missing ConfigMap/Secret.
// If after 5m they are not still available, the function errors out
func RemoteExecution(ctx context.Context, config VirtualKubeletConfig, p *VirtualKubeletProvider, pod *v1.Pod, mode int8) error {

	b, err := os.ReadFile(config.VKTokenFile) // just pass the file name
	if err != nil {
		log.G(ctx).Fatal(err)
		return err
	}
	token := string(b)

	switch mode {
	case CREATE:
		var req commonIL.PodCreateRequests
		var resp commonIL.CreateStruct
		req.Pod = *pod
		startTime := time.Now()

		for {
			timeNow := time.Now()
			if timeNow.Sub(startTime).Seconds() < time.Hour.Minutes()*5 {

				_, err := p.clientSet.CoreV1().Pods(pod.Namespace).Get(ctx, pod.Name, metav1.GetOptions{})
				if err != nil {
					log.G(ctx).Warning("Deleted Pod before actual creation")
					return nil
				}

				var failed bool

				for _, volume := range pod.Spec.Volumes {

					if volume.ConfigMap != nil {
						cfgmap, err := p.clientSet.CoreV1().ConfigMaps(pod.Namespace).Get(ctx, volume.ConfigMap.Name, metav1.GetOptions{})
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
						scrt, err := p.clientSet.CoreV1().Secrets(pod.Namespace).Get(ctx, volume.Secret.SecretName, metav1.GetOptions{})
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
				for i, _ := range pod.Status.ContainerStatuses {
					pod.Status.ContainerStatuses[i].Ready = false
				}
				p.UpdatePod(ctx, pod)
				return errors.New("unable to retrieve ConfigMaps or Secrets. Check logs")
			}
		}

		returnVal, err := createRequest(config, req, token)
		if err != nil {
			return err
		}

		err = json.Unmarshal(returnVal, &resp)
		if err != nil {
			return err
		}

		if string(pod.UID) == resp.PodUID && err != nil {
			if pod.Annotations == nil {
				pod.Annotations = map[string]string{}
			}
			pod.Annotations["JobID"] = resp.PodJID
		}

		err = p.UpdatePod(ctx, pod)
		if err != nil {
			return err
		}

		log.G(ctx).Info("Pod " + pod.Name + " created successfully and with Job ID " + resp.PodJID)
		log.G(ctx).Debug(string(returnVal))

	case DELETE:
		req := pod
		if pod.Status.Phase != "Initializing" {
			returnVal, err := deleteRequest(config, req, token)
			if err != nil {
				return err
			}
			log.G(ctx).Info(string(returnVal))
		}
	}
	return nil
}

// checkPodsStatus is regularly called by the VK itself at regular intervals of time to query InterLink for Pods' status.
// It basically append all available pods registered to the VK to a slice and passes this slice to the statusRequest function.
// After the statusRequest returns a response, this function uses that response to update every Pod and Container status.
func checkPodsStatus(ctx context.Context, p *VirtualKubeletProvider, podsList []*v1.Pod, token string, config VirtualKubeletConfig) ([]commonIL.PodStatus, error) {
	var returnVal []byte
	var ret []commonIL.PodStatus
	var err error

	//log.G(ctx).Debug(p.pods) //commented out because it's too verbose. uncomment to see all registered pods

	returnVal, err = statusRequest(config, podsList, token)

	if err != nil {
		return nil, err
	} else if returnVal != nil {
		err = json.Unmarshal(returnVal, &ret)
		if err != nil {
			return nil, err
		}
		if podsList != nil {
			for _, podStatus := range ret {

				if podStatus.PodName != "" {
					pod, err := p.GetPod(ctx, podStatus.PodNamespace, podStatus.PodName)
					if err != nil {
						log.G(ctx).Warning(err)
						continue
					}

				if podStatus.PodUID == string(pod.UID) {
					podRunning := false
					podErrored := false
					podCompleted := false
					failedReason := ""
					terminatedContainers := 0
					for _, containerStatus := range podStatus.Containers {
						index := 0
						foundCt := false

						for i, checkedContainer := range pod.Status.ContainerStatuses {
							if checkedContainer.Name == containerStatus.Name {
								foundCt = true
								index = i
							}

							if !foundCt {
								pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, containerStatus)
							} else {
								pod.Status.ContainerStatuses[index] = containerStatus
							}

							if containerStatus.State.Terminated != nil {
								log.G(ctx).Debug("Pod " + podStatus.PodName + ": Service " + containerStatus.Name + " is not running on Sidecar")
								terminatedContainers++
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

						if terminatedContainers == len(podStatus.Containers) {
              podCompleted = true
            }

            if podRunning && pod.Status.Phase != v1.PodRunning {
              pod.Status.Phase = v1.PodRunning
              pod.Status.Conditions = append(pod.Status.Conditions, v1.PodCondition{Type: v1.PodReady, Status: v1.ConditionTrue})
            } else if podErrored && pod.Status.Phase != v1.PodFailed {
              pod.Status.Phase = v1.PodFailed
              pod.Status.Reason = failedReason
            } else if podCompleted && pod.Status.Phase != v1.PodSucceeded {
              pod.Status.Conditions = append(pod.Status.Conditions, v1.PodCondition{Type: v1.PodReady, Status: v1.ConditionFalse})
              pod.Status.Phase = v1.PodSucceeded
              pod.Status.Reason = "Completed"
						}

						err = p.UpdatePod(ctx, pod)
						if err != nil {
							log.G(ctx).Error(err)
							return nil, err
						}
				} else {
					list, err := p.clientSet.CoreV1().Pods(podStatus.PodNamespace).List(ctx, metav1.ListOptions{})
					if err != nil {
						log.G(ctx).Error(err)
						return nil, err
					}

					pods := list.Items

					for _, pod := range pods {
						if string(pod.UID) == podStatus.PodUID {
							err = updateCacheRequest(config, pod, token)
							if err != nil {
								log.G(ctx).Error(err)
								continue
							}
						}
					}

				}
			}

			log.G(ctx).Info("No errors while getting statuses")
			log.G(ctx).Debug(ret)
			return nil, nil
		} else {
			return ret, err
		}

	}

	return nil, err
}
