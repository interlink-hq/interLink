package virtualkubelet

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"

	commonIL "github.com/intertwin-eu/interlink/pkg/common"

	exec "github.com/alexellis/go-execute/pkg/v1"
	"github.com/containerd/containerd/log"
	v1 "k8s.io/api/core/v1"
)

var NoReq uint8

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

	returnValue, _ = ioutil.ReadAll(resp.Body)

	if string(returnValue) != "200" {
		log.G(context.Background()).Error("Unexpeceted code received: " + string(returnValue))
	}

	return returnValue, nil
}

func deleteRequest(pods []*v1.Pod, token string) ([]byte, error) {
	var returnValue, _ = json.Marshal(commonIL.PodStatus{PodStatus: commonIL.UNKNOWN})

	bodyBytes, err := json.Marshal(pods)
	if err != nil {
		log.L.Error(err)
		return nil, err
	}
	reader := bytes.NewReader(bodyBytes)
	req, err := http.NewRequest(http.MethodDelete, commonIL.InterLinkConfigInst.Interlinkurl+":"+commonIL.InterLinkConfigInst.Interlinkport+"/delete", reader)
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

	returnValue, _ = ioutil.ReadAll(resp.Body)
	var response commonIL.PodStatus
	err = json.Unmarshal(returnValue, &response)
	if err != nil {
		log.L.Error(err)
		return nil, err
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
		log.L.Error(err)
		return nil, err
	}

	returnValue, _ = ioutil.ReadAll(resp.Body)
	if err != nil {
		log.L.Error(err)
		return nil, err
	}

	return returnValue, nil
}

func RemoteExecution(p *VirtualKubeletProvider, ctx context.Context, mode int8, imageLocation string, pod *v1.Pod, container v1.Container) error {
	var req []*v1.Pod
	req = []*v1.Pod{pod}

	b, err := os.ReadFile(commonIL.InterLinkConfigInst.VKTokenFile) // just pass the file name
	if err != nil {
		fmt.Print(err)
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
		if NoReq > 0 {
			NoReq--
		} else {
			returnVal, err := deleteRequest(req, token)
			if err != nil {
				log.G(ctx).Error(err)
				return err
			}
			log.G(ctx).Info(string(returnVal))
		}
		break
	}
	return nil
}

func checkPodsStatus(p *VirtualKubeletProvider, ctx context.Context, token string) error {
	if len(p.pods) == 0 {
		return nil
	}
	var returnVal []byte
	var ret commonIL.StatusResponse
	var PodsList []*v1.Pod

	for _, pod := range p.pods {
		PodsList = append(PodsList, pod)
	}
	log.G(ctx).Info(p.pods)

	returnVal, err := statusRequest(PodsList, token)
	if err != nil {
		log.G(ctx).Error(err)
		return err
	}

	err = json.Unmarshal(returnVal, &ret)
	if err != nil {
		log.G(ctx).Error(err)
		return err
	}

	for podIndex, podStatus := range ret.PodStatus {
		if podStatus.PodStatus == 1 {
			NoReq++
			cmd := []string{"delete pod " + ret.PodStatus[podIndex].PodName + " -n vk"}
			shell := exec.ExecTask{
				Command: "kubectl",
				Args:    cmd,
				Shell:   true,
			}

			execReturn, _ := shell.Execute()
			if execReturn.Stderr != "" {
				log.G(ctx).Error(fmt.Errorf("Could not delete pod " + ret.PodStatus[podIndex].PodName))
				return fmt.Errorf("Could not delete pod " + ret.PodStatus[podIndex].PodName)
			}
		}
	}

	log.L.Println(ret)
	return nil
}
