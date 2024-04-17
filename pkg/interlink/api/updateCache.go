package api

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/containerd/containerd/log"
	commonIL "github.com/intertwin-eu/interlink/pkg/interlink"
	v1 "k8s.io/api/core/v1"
)

// UpdateCacheHandler is responsible for deleting not-available-anymore Pods on the Virtual Kubelet from the InterLink caching structure
func (h *InterLinkHandler) UpdateCacheHandler(w http.ResponseWriter, r *http.Request) {
	log.G(h.Ctx).Info("InterLink: received UpdateCache call")
	var pod v1.Pod

	bodyBytes, err := io.ReadAll(r.Body)
	statusCode := http.StatusOK
	if err != nil {
		statusCode = http.StatusInternalServerError
		log.G(h.Ctx).Error(err)
	}

	err = json.Unmarshal(bodyBytes, &pod)
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		log.G(h.Ctx).Error(err)
		return
	}

	podStatus := []commonIL.PodStatus{{PodName: pod.Name, PodUID: string(pod.UID), PodNamespace: pod.Namespace, Containers: pod.Status.ContainerStatuses}}

	err = updateStatuses(h.Config, podStatus)
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		log.G(h.Ctx).Error(err)
		return
	}

	w.WriteHeader(statusCode)
	w.Write([]byte("Updated cache"))
}
