package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/containerd/containerd/log"
	"go.opentelemetry.io/otel/attribute"
	v1 "k8s.io/api/core/v1"

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

	// The body is never recorded verbatim. The VK posts the whole pod as JSON
	// here, so copying it into an attribute put tens of kilobytes of
	// client-supplied content on the span: enough for a large body to push the
	// OTLP export past its message size limit and have the batch processor drop
	// every span batched with it. Only the pod identity is recorded, and only
	// when the body really is a pod.
	var pod *v1.Pod
	if err := json.Unmarshal(bodyBytes, &pod); err == nil && pod != nil {
		setPodSpanAttributes(span, pod, h.Config.Tracing.Detailed)
	} else {
		span.SetAttributes(attribute.Bool("interlink.update_cache.body.is_pod", false))
	}

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
