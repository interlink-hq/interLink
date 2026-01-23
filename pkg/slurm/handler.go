package slurm

import (
	"context"
	"net/http"

	"github.com/containerd/containerd/log"
)

// handleError is a minimal helper used by the SubmitHandler in this package.
func (h *SidecarHandler) handleError(ctx context.Context, w http.ResponseWriter, status int, err error) {
	if err != nil {
		log.G(ctx).Error(err)
	}
	if w == nil {
		return
	}
	w.WriteHeader(status)
	if err != nil {
		if _, writeErr := w.Write([]byte(err.Error())); writeErr != nil {
			log.G(ctx).Error(writeErr)
		}
	}
}
