package interlink

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"net/http"
	"time"

	"github.com/containerd/containerd/log"
	commonIL "github.com/intertwin-eu/interlink/pkg/common"
	v1 "k8s.io/api/core/v1"
)

func CreateHandler(w http.ResponseWriter, r *http.Request) {
	log.G(Ctx).Info("InterLink: received Create call")
	bodyBytes, err := ioutil.ReadAll(r.Body)
	statusCode := http.StatusOK
	if err != nil {
		statusCode = http.StatusInternalServerError
		log.G(Ctx).Fatal(err)
	}

	var req *http.Request //request to forward to sidecar
	var req2 []*v1.Pod    //request for interlink
	json.Unmarshal(bodyBytes, &req2)

	var retrieved_data []commonIL.RetrievedPodData
	for _, pod := range req2 {
		check := true //the following loop is used to add a pod to the list of to be created pods. this is to avoid multiple calls
		for _, s := range ToBeCreated {
			if pod.Name == s {
				check = false
			}
		}
		if check {
			ToBeCreated = append(ToBeCreated, pod.Name)
			data := []commonIL.RetrievedPodData{}
			if commonIL.InterLinkConfigInst.ExportPodData {
				data, err = getData(pod)
				if err != nil {
					statusCode = http.StatusInternalServerError
					w.WriteHeader(statusCode)
					return
				}
				log.G(Ctx).Debug(data)
			}

			if data == nil {
				data = append(data, commonIL.RetrievedPodData{Pod: *pod})
			}

			retrieved_data = append(retrieved_data, data...)
		} else {
			log.G(Ctx).Warning("Submitted pods are still being created...")
		}
	}

	if retrieved_data != nil {
		bodyBytes, err = json.Marshal(retrieved_data)
		log.G(Ctx).Debug(string(bodyBytes))
		reader := bytes.NewReader(bodyBytes)

		switch commonIL.InterLinkConfigInst.Sidecarservice {
		case "docker":
			req, err = http.NewRequest(http.MethodPost, commonIL.InterLinkConfigInst.Sidecarurl+":"+commonIL.InterLinkConfigInst.Sidecarport+"/create", reader)

		case "slurm":
			req, err = http.NewRequest(http.MethodPost, commonIL.InterLinkConfigInst.Sidecarurl+":"+commonIL.InterLinkConfigInst.Sidecarport+"/submit", reader)

		default:
			break
		}

		if err != nil {
			statusCode = http.StatusInternalServerError
			w.WriteHeader(statusCode)
			log.G(Ctx).Fatal(err)
		}

		log.G(Ctx).Info("InterLink: forwarding Create call to sidecar")
		var resp *http.Response
		for {
			resp, err = http.DefaultClient.Do(req)
			if err != nil {
				log.G(Ctx).Error(err)
				time.Sleep(time.Second * 5)
			} else {
				break
			}
		}

		statusCode = resp.StatusCode

		if resp.StatusCode == http.StatusOK {
			statusCode = http.StatusOK
			log.G(Ctx).Debug(statusCode)
		} else {
			statusCode = http.StatusInternalServerError
			log.G(Ctx).Debug(statusCode)
		}

		returnValue, _ := ioutil.ReadAll(resp.Body)
		log.G(Ctx).Debug(string(returnValue))
		w.WriteHeader(statusCode)
		w.Write(returnValue)

		if resp.StatusCode == http.StatusOK {
			temp := ToBeCreated
			for _, data := range retrieved_data {
				for j, podName := range ToBeCreated {
					if podName == data.Pod.Name {
						temp = append(ToBeCreated[:j], ToBeCreated[j+1:]...)
					}
				}
			}
			ToBeCreated = temp
		}
	}
}
