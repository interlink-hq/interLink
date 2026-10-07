package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/containerd/containerd/log"
	v1 "k8s.io/api/core/v1"

	types "github.com/interlink-hq/interlink/pkg/interlink"
)

// DeleteHandler handles HTTP DELETE requests to remove pods from remote systems.
// This endpoint processes pod deletion requests from the Virtual Kubelet by:
//  1. Removing the pod from the local status cache
//  2. Forwarding the deletion request to the sidecar plugin
//
// The handler ensures cleanup of both local state and remote resources.
//
// Request body: JSON-encoded v1.Pod object
// Response: Success or error status from the sidecar plugin
//
// HTTP Status Codes:
//   - 200: Pod deletion request processed successfully
//   - 500: Internal server error (sidecar communication failures, JSON unmarshalling errors)
func (h *InterLinkHandler) DeleteHandler(w http.ResponseWriter, r *http.Request) {
	start := time.Now().UnixMicro()
	ctx, span, sessionContext := h.startAPITrace(r, "DeleteAPI", "/delete", start)
	defer span.End()
	defer types.SetDurationSpan(start, span)
	defer types.SetInfoFromHeaders(span, &r.Header)

	log.G(h.Ctx).Info("InterLink: received Delete call")

	bodyBytes, err := io.ReadAll(r.Body)
	var statusCode int

	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		log.G(h.Ctx).Error(err)
		types.SetSpanError(span, statusCode, err)
		return
	}
	setRequestBodySize(span, bodyBytes)

	var req *http.Request
	var pod *v1.Pod
	reader := bytes.NewReader(bodyBytes)
	err = json.Unmarshal(bodyBytes, &pod)
	if err != nil {
		statusCode = http.StatusBadRequest
		w.WriteHeader(statusCode)
		log.G(h.Ctx).Error(err)
		types.SetSpanError(span, statusCode, err)
		return
	}
	if pod == nil {
		statusCode = http.StatusBadRequest
		err = errors.New("request body must contain a pod")
		w.WriteHeader(statusCode)
		log.G(h.Ctx).Error(err)
		types.SetSpanError(span, statusCode, err)
		return
	}

	setPodSpanAttributes(span, pod, h.Config.Tracing.Detailed)

	deleteCachedStatus(string(pod.UID))
	req, err = http.NewRequestWithContext(ctx, http.MethodPost, h.SidecarEndpoint+"/delete", reader)
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		log.G(h.Ctx).Error(err)
		types.SetSpanError(span, statusCode, err)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	log.G(h.Ctx).Info("InterLink: forwarding Delete call to sidecar")
	_, err = ReqWithError(ctx, req, w, start, span, true, false, sessionContext, h.ClientHTTP)
	if err != nil {
		// ReqWithError has already marked the span as failed.
		log.L.Error(err)
		return
	}

	// The return code was already recorded by ReqWithError, so only the outcome
	// is set here.
	types.SetSpanOK(span, 0)
}
