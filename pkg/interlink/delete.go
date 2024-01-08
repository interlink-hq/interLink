package interlink

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/containerd/containerd/log"
	commonIL "github.com/intertwin-eu/interlink/pkg/common"
	v1 "k8s.io/api/core/v1"
)

func DeleteHandler(w http.ResponseWriter, r *http.Request) {
	log.G(Ctx).Info("InterLink: received Delete call")

	bodyBytes, err := io.ReadAll(r.Body)
	statusCode := http.StatusOK

	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		log.G(Ctx).Fatal(err)
	}

	var req *http.Request
	var pod *v1.Pod
	reader := bytes.NewReader(bodyBytes)
	err = json.Unmarshal(bodyBytes, &pod)

	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		log.G(Ctx).Fatal(err)
	}

	req, err = http.NewRequest(http.MethodPost, commonIL.InterLinkConfigInst.Sidecarurl+":"+commonIL.InterLinkConfigInst.Sidecarport+"/delete", reader)

	log.G(Ctx).Info("InterLink: forwarding Delete call to sidecar")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		log.G(Ctx).Error(err)
		return
	}

	returnValue, _ := io.ReadAll(resp.Body)
	statusCode = resp.StatusCode

	if statusCode != http.StatusOK {
		w.WriteHeader(http.StatusInternalServerError)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	log.G(Ctx).Debug("InterLink: " + string(returnValue))
	var returnJson []commonIL.PodStatus
	returnJson = append(returnJson, commonIL.PodStatus{PodName: pod.Name, PodUID: string(pod.UID), PodNamespace: pod.Namespace})

	bodyBytes, err = json.Marshal(returnJson)
	if err != nil {
		log.G(Ctx).Error(err)
		w.Write([]byte{})
	} else {
		w.Write(bodyBytes)
	}

}
