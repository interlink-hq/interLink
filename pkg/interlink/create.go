package interlink

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/containerd/containerd/log"
	commonIL "github.com/intertwin-eu/interlink/pkg/common"
	v1 "k8s.io/api/core/v1"
)

func CreateHandler(w http.ResponseWriter, r *http.Request) {
	log.G(Ctx).Info("InterLink: received Create call")

	bodyBytes, err := io.ReadAll(r.Body)
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
		data = []commonIL.RetrievedPodData{}
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

		if retrieved_data != nil {
			bodyBytes, err = json.Marshal(retrieved_data)
			log.G(Ctx).Debug(string(bodyBytes))
			reader := bytes.NewReader(bodyBytes)

			req, err = http.NewRequest(http.MethodPost, commonIL.InterLinkConfigInst.Sidecarurl+":"+commonIL.InterLinkConfigInst.Sidecarport+"/create", reader)

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

			returnValue, _ := io.ReadAll(resp.Body)
			log.G(Ctx).Debug(string(returnValue))
			w.WriteHeader(statusCode)
			w.Write(returnValue)
		}
	}
}
