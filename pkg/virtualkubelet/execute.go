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

	commonIL "github.com/intertwin-eu/interlink/pkg/common"

	exec "github.com/alexellis/go-execute/pkg/v1"
	"github.com/containerd/containerd/log"
	v1 "k8s.io/api/core/v1"
)

func createRequest(pods []*v1.Pod, token string) ([]byte, error) {
	var returnValue, _ = json.Marshal(commonIL.PodStatus{PodStatus: commonIL.UNKNOWN})

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
	returnValue, _ := json.Marshal(commonIL.PodStatus{PodStatus: commonIL.UNKNOWN})

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
		returnValue, _ = os.ReadAll(resp.Body)
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

func RemoteExecution(p *VirtualKubeletProvider, ctx context.Context, mode int8, imageLocation string, pod *v1.Pod, container v1.Container) error {
	var req []*v1.Pod
	req = []*v1.Pod{pod}

	b, err := os.ReadFile(commonIL.InterLinkConfigInst.VKTokenFile) // just pass the file name
	if err != nil {
		log.G(ctx).Fatal(err)
	}
	token := string(b)

	switch mode {
	case CREATE:
		//v1.Pod used only for secrets and volumes management; TO BE IMPLEMENTED
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
			log.G(ctx).Error(err)
			return err
		}

		for i, podStatus := range ret {
			if podStatus.PodStatus == 1 {
				cmd := []string{"delete pod " + ret[i].PodName + " -n " + ret[i].PodNamespace}
				shell := exec.ExecTask{
					Command: "kubectl",
					Args:    cmd,
					Shell:   true,
				}

				execReturn, _ := shell.Execute()
				if execReturn.Stderr != "" {
					log.G(ctx).Error(fmt.Errorf("Could not delete pod " + ret[i].PodName))
					return fmt.Errorf("Could not delete pod " + ret[i].PodName)
				}
			}
		}

		log.G(ctx).Info("No errors while getting statuses")
		log.G(ctx).Debug(ret)
		return nil
	}
	return err
}
