package interlink

import (
	"io"
	"net/http"

	"github.com/containerd/containerd/log"
)

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
