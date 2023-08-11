package interlink

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"net/http"

	"github.com/containerd/containerd/log"
	commonIL "github.com/intertwin-eu/interlink/pkg/common"
	v1 "k8s.io/api/core/v1"
)

func DeleteHandler(w http.ResponseWriter, r *http.Request) {
	log.G(Ctx).Info("InterLink: received Delete call")
	bodyBytes, err := ioutil.ReadAll(r.Body)
	statusCode := http.StatusOK

	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		log.G(Ctx).Fatal(err)
	}

	var req *http.Request
	var pods []*v1.Pod
	reader := bytes.NewReader(bodyBytes)
	json.Unmarshal(bodyBytes, &pods)

	for _, pod := range pods {
		check := true //the following loop is used to add a pod to the list of to be deleted pods. this is to avoid multiple calls
		for _, s := range ToBeDeleted {
			if pod.Name == s {
				check = false
			}
		}
		if check {
			ToBeDeleted = append(ToBeDeleted, pod.Name)

			switch commonIL.InterLinkConfigInst.Sidecarservice {
			case "docker":
				req, err = http.NewRequest(http.MethodPost, commonIL.InterLinkConfigInst.Sidecarurl+":"+commonIL.InterLinkConfigInst.Sidecarport+"/delete", reader)

			case "slurm":
				req, err = http.NewRequest(http.MethodPost, commonIL.InterLinkConfigInst.Sidecarurl+":"+commonIL.InterLinkConfigInst.Sidecarport+"/stop", reader)

			default:
				break
			}

			temp := ToBeDeleted
			for _, pod := range pods {
				for j, podName := range ToBeDeleted {
					if podName == pod.Name {
						temp = append(ToBeDeleted[:j], ToBeDeleted[j+1:]...)
					}
				}
			}
			ToBeDeleted = temp

			log.G(Ctx).Info("InterLink: forwarding Delete call to sidecar")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				statusCode = http.StatusInternalServerError
				w.WriteHeader(statusCode)
				log.G(Ctx).Error(err)
				return
			}

			returnValue, _ := ioutil.ReadAll(resp.Body)
			statusCode = resp.StatusCode

			if statusCode != http.StatusOK {
				w.WriteHeader(http.StatusInternalServerError)
				ToBeDeleted = append(ToBeDeleted, pod.Name)
			} else {
				w.WriteHeader(http.StatusOK)
			}
			log.G(Ctx).Debug("InterLink: " + string(returnValue))
			var returnJson []commonIL.PodStatus
			for _, pod := range pods {
				returnJson = append(returnJson, commonIL.PodStatus{PodName: pod.Name, PodNamespace: pod.Namespace, PodStatus: commonIL.STOP})
			}
			bodyBytes, err = json.Marshal(returnJson)
			if err != nil {
				log.G(Ctx).Error(err)
				w.Write([]byte{})
			} else {
				w.Write(bodyBytes)
			}

		} else {
			w.WriteHeader(http.StatusOK)
		}
	}
}
