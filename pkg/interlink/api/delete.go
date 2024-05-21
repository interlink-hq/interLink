package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/containerd/containerd/log"
	v1 "k8s.io/api/core/v1"

	commonIL "github.com/intertwin-eu/interlink/pkg/interlink"
)

// DeleteHandler deletes the cached status for the provided Pod and forwards the request to the sidecar
func (h *InterLinkHandler) DeleteHandler(w http.ResponseWriter, r *http.Request) {
	log.G(h.Ctx).Info("InterLink: received Delete call")
	bodyBytes, err := io.ReadAll(r.Body)
	statusCode := http.StatusOK

	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		log.G(h.Ctx).Fatal(err)
	}

	var req *http.Request
	var pod *v1.Pod
	reader := bytes.NewReader(bodyBytes)
	err = json.Unmarshal(bodyBytes, &pod)

	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		log.G(h.Ctx).Fatal(err)
	}

	err = deleteCachedStatus(h.Config, string(pod.UID))
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		log.G(h.Ctx).Error(err)
		return
	}
	req, err = http.NewRequest(http.MethodPost, h.Config.Sidecarurl+":"+h.Config.Sidecarport+"/delete", reader)
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		log.G(h.Ctx).Error(err)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	log.G(h.Ctx).Info("InterLink: forwarding Delete call to sidecar")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		log.G(h.Ctx).Error(err)
		return
	}

	returnValue, _ := io.ReadAll(resp.Body)
	statusCode = resp.StatusCode

	log.G(h.Ctx).Debug("InterLink: " + string(returnValue))

	var returnJson []commonIL.PodStatus
	returnJson = append(returnJson, commonIL.PodStatus{PodName: pod.Name, PodUID: string(pod.UID), PodNamespace: pod.Namespace})

	if statusCode != http.StatusOK {
		w.WriteHeader(http.StatusInternalServerError)
	} else {
		w.WriteHeader(http.StatusOK)
	}

	bodyBytes, err = json.Marshal(returnJson)
	if err != nil {
		log.G(h.Ctx).Error(err)
		w.Write([]byte{})
	} else {
		w.Write(bodyBytes)
	}

}
