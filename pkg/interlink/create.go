package interlink

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/containerd/containerd/log"

	commonIL "github.com/intertwin-eu/interlink/pkg/common"
)

func (h *InterLinkHandler) CreateHandler(w http.ResponseWriter, r *http.Request) {
	log.G(Ctx).Info("InterLink: received Create call")

	statusCode := -1

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		log.G(Ctx).Fatal(err)
		return
	}

	var req *http.Request              //request to forward to sidecar
	var pod commonIL.PodCreateRequests //request for interlink
	err = json.Unmarshal(bodyBytes, &pod)
	if err != nil {
		statusCode = http.StatusInternalServerError
		log.G(Ctx).Fatal(err)
		w.WriteHeader(statusCode)
		return
	}

	var retrievedData []commonIL.RetrievedPodData

	data := commonIL.RetrievedPodData{}
	if h.Config.ExportPodData {
		data, err = getData(h.Config, pod)
		if err != nil {
			statusCode = http.StatusInternalServerError
			log.G(Ctx).Fatal(err)
			w.WriteHeader(statusCode)
			return
		}
	}

	retrievedData = append(retrievedData, data)

	if retrievedData != nil {
		bodyBytes, err = json.Marshal(retrievedData)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			log.G(Ctx).Fatal(err)
			return
		}
		log.G(Ctx).Debug(string(bodyBytes))
		reader := bytes.NewReader(bodyBytes)

		req, err = http.NewRequest(http.MethodPost, h.Config.Sidecarurl+":"+h.Config.Sidecarport+"/create", reader)

		if err != nil {
			statusCode = http.StatusInternalServerError
			w.WriteHeader(statusCode)
			log.G(Ctx).Fatal(err)
			return
		}

		log.G(Ctx).Info("InterLink: forwarding Create call to sidecar")
		var resp *http.Response

		req.Header.Set("Content-Type", "application/json")
		resp, err = http.DefaultClient.Do(req)
		if err != nil {
			statusCode = http.StatusInternalServerError
			w.WriteHeader(statusCode)
			log.G(Ctx).Error(err)
			return
		}

		if resp.StatusCode == http.StatusOK {
			statusCode = http.StatusOK
			log.G(Ctx).Debug(statusCode)
		} else {
			statusCode = http.StatusInternalServerError
			log.G(Ctx).Error(statusCode)
		}

		returnValue, _ := io.ReadAll(resp.Body)
		log.G(Ctx).Debug(string(returnValue))
		w.WriteHeader(statusCode)
		w.Write(returnValue)
	}
}
