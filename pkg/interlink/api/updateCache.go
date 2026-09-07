package api

import (
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/containerd/containerd/log"
	"go.opentelemetry.io/otel/attribute"

	types "github.com/interlink-hq/interlink/pkg/interlink"
)

// UpdateCacheHandler is responsible for deleting not-available-anymore Pods on the Virtual Kubelet from the InterLink caching structure
func (h *InterLinkHandler) UpdateCacheHandler(w http.ResponseWriter, r *http.Request) {
	start := time.Now().UnixMicro()
	_, span, _ := h.startAPITrace(r, "UpdateCacheAPI", "/updateCache", start)
	defer span.End()
	defer types.SetDurationSpan(start, span)
	defer types.SetInfoFromHeaders(span, &r.Header)

	log.G(h.Ctx).Info("InterLink: received UpdateCache call")

	bodyBytes, err := io.ReadAll(r.Body)
	statusCode := http.StatusOK
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		log.G(h.Ctx).Error(err)
		types.SetSpanError(span, statusCode, err)
		return
	}
	setRequestBodySize(span, bodyBytes)
	span.SetAttributes(attribute.String("pod.uid", string(bodyBytes)))

	deleteCachedStatus(string(bodyBytes))

	w.WriteHeader(statusCode)
	_, err = w.Write([]byte("Updated cache"))
	if err != nil {
		errWrite := errors.New("failed to write to http buffer")
		log.G(h.Ctx).Error(errWrite)
		types.SetSpanError(span, statusCode, errWrite)
		return
	}
	types.SetSpanOK(span, statusCode)
}
