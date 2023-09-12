package interlink

import (
	"net/http"

	"github.com/containerd/containerd/log"
)

func Ping(w http.ResponseWriter, r *http.Request) {
	log.G(Ctx).Info("InterLink: received Ping call")
	w.WriteHeader(http.StatusOK)
}
