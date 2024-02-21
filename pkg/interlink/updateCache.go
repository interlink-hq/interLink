package interlink

import (
	"io"
	"net/http"

	"github.com/containerd/containerd/log"
)

// UpdateCacheHandler is responsible for deleting not-available-anymore Pods on the Virtual Kubelet from the InterLink caching structure
func UpdateCacheHandler(w http.ResponseWriter, r *http.Request) {
	log.G(Ctx).Info("InterLink: received UpdateCache call")

	bodyBytes, err := io.ReadAll(r.Body)
	statusCode := http.StatusOK
	if err != nil {
		statusCode = http.StatusInternalServerError
		log.G(Ctx).Fatal(err)
	}

	deleteCachedStatus(string(bodyBytes))

	w.WriteHeader(statusCode)
	w.Write([]byte("Updated cache"))
}
