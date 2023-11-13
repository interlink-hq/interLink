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

	commonIL "github.com/intertwin-eu/interlink/pkg/common"

	"github.com/containerd/containerd/log"
	v1 "k8s.io/api/core/v1"
)

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
		//pod.Spec.NodeSelector["kubernetes.io/hostname"] = "emptyNode"
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
			//log.G(ctx).Debug(pod)
			if err != nil {
				log.G(ctx).Error(err)
				return err
			}


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
