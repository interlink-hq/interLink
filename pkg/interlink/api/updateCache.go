package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/containerd/containerd/log"
	v1 "k8s.io/api/core/v1"

	types "github.com/interlink-hq/interlink/pkg/interlink"
)

// UpdateCacheHandler is responsible for deleting not-available-anymore Pods on the Virtual Kubelet from the InterLink caching structure
func (h *InterLinkHandler) UpdateCacheHandler(w http.ResponseWriter, r *http.Request) {
	start := time.Now().UnixMicro()
	_, span, _ := h.startAPITrace(r, "UpdateCacheAPI", "/updateCache", start)
	defer span.End()
	defer types.SetDurationSpan(start, span)
	// X-Forwarded-Email and X-Forwarded-User identify a person, so they follow
	// the same opt-in rule as the other client details.
	if h.Config.Tracing.Detailed {
		defer types.SetInfoFromHeaders(span, &r.Header)
	}

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

	// The body is the pod to forget, as JSON: the VK marshals it in
	// updateCacheRequest. It is never recorded verbatim, because copying it into
	// an attribute put tens of kilobytes of client-supplied content on the span,
	// enough for a large body to push the OTLP export past its message size
	// limit and have the batch processor drop every span batched with it. Only
	// the pod identity goes on the span.
	var pod *v1.Pod
	if err := json.Unmarshal(bodyBytes, &pod); err != nil || pod == nil {
		statusCode = http.StatusBadRequest
		errBody := fmt.Errorf("request body must contain a pod: %w", err)
		w.WriteHeader(statusCode)
		log.G(h.Ctx).Error(errBody)
		types.SetSpanError(span, statusCode, errBody)
		return
	}
	setPodSpanAttributes(span, pod, h.Config.Tracing.Detailed)

	// Keyed by pod UID, like every other entry: deleteCachedStatus used to be
	// given the whole body, which matches no key, so the invalidation this
	// handler exists for never happened and the call still answered 200.
	deleteCachedStatus(string(pod.UID))

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
