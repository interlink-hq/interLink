package interlink

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/containerd/containerd/log"
	v1 "k8s.io/api/core/v1"

	commonIL "github.com/intertwin-eu/interlink/pkg/common"
)

func (h *InterLinkHandler) StatusHandler(w http.ResponseWriter, r *http.Request) {
	statusCode := http.StatusOK
	var pods []*v1.Pod
	log.G(Ctx).Info("InterLink: received GetStatus call")

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		log.G(Ctx).Fatal(err)
	}

	err = json.Unmarshal(bodyBytes, &pods)
	if err != nil {
		log.G(Ctx).Error(err)
	}

	var podsToBeChecked []*v1.Pod
	var returnedStatuses []commonIL.PodStatus //returned from the query to the sidecar
	var returnPods []commonIL.PodStatus       //returned to the vk

	PodStatuses.mu.Lock()
	for _, pod := range pods {
		cached := checkIfCached(string(pod.UID))
		if pod.Status.Phase == v1.PodRunning || pod.Status.Phase == v1.PodPending || !cached {
			podsToBeChecked = append(podsToBeChecked, pod)
		}
	}
	PodStatuses.mu.Unlock()

	bodyBytes, err = json.Marshal(podsToBeChecked)
	if err != nil {
		log.G(Ctx).Fatal(err)
	}

	reader := bytes.NewReader(bodyBytes)
	req, err := http.NewRequest(http.MethodGet, h.Config.Sidecarurl+":"+h.Config.Sidecarport+"/status", reader)
	if err != nil {
		log.G(Ctx).Fatal(err)
	}

	log.G(Ctx).Info("InterLink: forwarding GetStatus call to sidecar")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		log.G(Ctx).Error(err)
		return
	}

	if resp.StatusCode != http.StatusOK {
		log.L.Error("Unexpected error occured. Status code: " + strconv.Itoa(resp.StatusCode) + ". Check Sidecar's logs for further informations")
		statusCode = http.StatusInternalServerError
	}

	bodyBytes, err = io.ReadAll(resp.Body)
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		log.G(Ctx).Error(err)
		return
	}
	err = json.Unmarshal(bodyBytes, &returnedStatuses)
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		log.G(Ctx).Error(err)
		return
	}

	updateStatuses(returnedStatuses)

	for _, pod := range pods {
		PodStatuses.mu.Lock()
		for _, cached := range PodStatuses.Statuses {
			if cached.PodUID == string(pod.UID) {
				returnPods = append(returnPods, cached)
				break
			}
		}
		PodStatuses.mu.Unlock()
	}

	returnValue, err := json.Marshal(returnPods)
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		log.G(Ctx).Error(err)
		return
	}
	log.G(Ctx).Debug("InterLink: status " + string(returnValue))

	w.WriteHeader(statusCode)
	w.Write(returnValue)
}
