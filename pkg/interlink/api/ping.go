package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/containerd/containerd/log"
	v1 "k8s.io/api/core/v1"
)

// Ping is just a very basic Ping function
func (h *InterLinkHandler) Ping(w http.ResponseWriter, r *http.Request) {
	log.G(h.Ctx).Info("InterLink: received Ping call")

	podsToBeChecked := []*v1.Pod{}
	bodyBytes, err := json.Marshal(podsToBeChecked)
	if err != nil {
		log.G(h.Ctx).Error(err)
	}

	reader := bytes.NewReader(bodyBytes)
	req, err := http.NewRequest(http.MethodGet, h.SidecarEndpoint+"/status", reader)
	if err != nil {
		log.G(h.Ctx).Error(err)
	}

	log.G(h.Ctx).Info("InterLink: forwarding GetStatus call to sidecar")
	req.Header.Set("Content-Type", "application/json")
	log.G(h.Ctx).Debug(req)
	_, err = http.DefaultClient.Do(req)
	if err != nil {
		log.G(h.Ctx).Error(err)
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(strconv.Itoa(http.StatusServiceUnavailable)))
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("0"))
}
