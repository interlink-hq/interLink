package api

import (
	"errors"
	"io"
	"net/http"

	"github.com/containerd/containerd/log"
)

// UpdateCacheHandler is responsible for deleting not-available-anymore Pods on the Virtual Kubelet from the InterLink caching structure
func (h *InterLinkHandler) UpdateCacheHandler(w http.ResponseWriter, r *http.Request) {
	log.G(h.Ctx).Info("InterLink: received UpdateCache call")

	bodyBytes, err := io.ReadAll(r.Body)
	statusCode := http.StatusOK
	if err != nil {
		statusCode = http.StatusInternalServerError
		log.G(h.Ctx).Fatal(err)
	}

	deleteCachedStatus(string(bodyBytes))

	w.WriteHeader(statusCode)
	_, err = w.Write([]byte("Updated cache"))
	if err != nil {
		log.G(h.Ctx).Error(errors.New("Failed to write to http buffer"))
	}

}
