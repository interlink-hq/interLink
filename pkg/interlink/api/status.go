package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/containerd/containerd/log"
	v1 "k8s.io/api/core/v1"

	types "github.com/interlink-hq/interlink/pkg/interlink"

	"go.opentelemetry.io/otel/attribute"
	trace "go.opentelemetry.io/otel/trace"
)

// StatusHandler handles HTTP GET requests to retrieve pod status information.
// This endpoint queries the sidecar plugin for the current status of one or more pods
// and implements intelligent caching to reduce unnecessary requests to the remote system.
//
// The handler maintains a local cache of pod statuses and only queries the sidecar for:
//   - Pods currently in Running or Pending state
//   - Pods not present in the cache
//
// Request body: JSON-encoded array of v1.Pod objects
// Response: JSON-encoded array of PodStatus objects
//
// HTTP Status Codes:
//   - 200: Status query completed successfully
//   - 500: Internal server error (sidecar communication failures, JSON marshalling errors)
func (h *InterLinkHandler) StatusHandler(w http.ResponseWriter, r *http.Request) {
	start := time.Now().UnixMicro()
	ctx, span, sessionContext := h.startAPITrace(r, "StatusAPI", "/status", start)
	defer span.End()
	defer types.SetDurationSpan(start, span)
	defer types.SetInfoFromHeaders(span, &r.Header)
	statusCode := http.StatusOK
	var pods []*v1.Pod
	log.G(h.Ctx).Info("InterLink: received GetStatus call")

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		log.G(h.Ctx).Error(err)
		types.SetSpanError(span, statusCode, err)
		return
	}
	setRequestBodySize(span, bodyBytes)

	err = json.Unmarshal(bodyBytes, &pods)
	if err != nil {
		errWithContext := fmt.Errorf("error doing fisrt Unmarshal() in StatusHandler() error detail: %s error: %w", fmt.Sprintf("%#v", err), err)
		log.G(h.Ctx).Error(errWithContext)
		statusCode = http.StatusBadRequest
		w.WriteHeader(statusCode)
		types.SetSpanError(span, statusCode, errWithContext)
		return
	}

	span.SetAttributes(
		attribute.Int("pods.count", len(pods)),
	)

	var podsToBeChecked []*v1.Pod
	var returnedStatuses []types.PodStatus // returned from the query to the sidecar
	var returnPods []types.PodStatus       // returned to the vk
	cacheHits := 0

	PodStatuses.mu.Lock()
	for _, pod := range pods {
		cached := checkIfCached(string(pod.UID))
		if cached {
			cacheHits++
		}
		if pod.Status.Phase == v1.PodRunning || pod.Status.Phase == v1.PodPending || !cached {
			podsToBeChecked = append(podsToBeChecked, pod)
		}
		if h.Config.Tracing.Detailed {
			span.AddEvent("Evaluated pod status cache", trace.WithAttributes(
				attribute.String("pod.name", pod.Name),
				attribute.String("pod.namespace", pod.Namespace),
				attribute.String("pod.uid", string(pod.UID)),
				attribute.String("pod.phase", string(pod.Status.Phase)),
				attribute.Bool("interlink.status.cache.hit", cached),
			))
		}
	}
	PodStatuses.mu.Unlock()
	span.SetAttributes(
		attribute.Int("interlink.status.cache.hit_count", cacheHits),
		attribute.Int("interlink.status.cache.miss_count", len(pods)-cacheHits),
		attribute.Int("interlink.status.plugin_query.count", len(podsToBeChecked)),
	)

	if len(podsToBeChecked) > 0 {

		bodyBytes, err = json.Marshal(podsToBeChecked)
		if err != nil {
			statusCode = http.StatusInternalServerError
			w.WriteHeader(statusCode)
			log.G(h.Ctx).Error(err)
			types.SetSpanError(span, statusCode, err)
			return
		}

		reader := bytes.NewReader(bodyBytes)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.SidecarEndpoint+"/status", reader)
		if err != nil {
			statusCode = http.StatusInternalServerError
			w.WriteHeader(statusCode)
			log.G(h.Ctx).Error(err)
			types.SetSpanError(span, statusCode, err)
			return
		}

		log.G(h.Ctx).Info("InterLink: forwarding GetStatus call to sidecar")
		req.Header.Set("Content-Type", "application/json")

		bodyBytes, err = ReqWithError(ctx, req, w, start, span, false, true, sessionContext, h.ClientHTTP)
		if err != nil {
			// ReqWithError has already marked the span as failed.
			log.L.Error(err)
			return
		}

		err = json.Unmarshal(bodyBytes, &returnedStatuses)
		if err != nil {
			statusCode = http.StatusInternalServerError
			w.WriteHeader(statusCode)
			errWithContext := fmt.Errorf("error doing Unmarshal() in StatusHandler() of req %s error detail: %s error: %w", fmt.Sprintf("%#v", req), fmt.Sprintf("%#v", err), err)
			log.G(h.Ctx).Error(errWithContext)
			types.SetSpanError(span, statusCode, errWithContext)
			return
		}

		updateStatuses(returnedStatuses)
	}

	if len(pods) > 0 {
		for _, pod := range pods {
			PodStatuses.mu.Lock()
			for _, cached := range PodStatuses.Statuses {
				if cached.PodUID == string(pod.UID) {
					returnPods = append(returnPods, cached)
					break
				}
			}
			PodStatuses.mu.Unlock()
		}
	} else {
		PodStatuses.mu.Lock()
		for _, pod := range PodStatuses.Statuses {
			returnPods = append(returnPods, pod)
		}
		PodStatuses.mu.Unlock()
	}

	returnValue, err := json.Marshal(returnPods)
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		log.G(h.Ctx).Error(err)
		types.SetSpanError(span, statusCode, err)
		return
	}
	span.SetAttributes(
		attribute.Int("interlink.status.returned.count", len(returnPods)),
		attribute.Int("http.response.body.size", len(returnValue)),
	)

	w.WriteHeader(statusCode)
	_, err = w.Write(returnValue)
	if err != nil {
		errWrite := errors.New("failed to write to http buffer")
		log.G(h.Ctx).Error(errWrite)
		types.SetSpanError(span, statusCode, errWrite)
		return
	}

	// Recorded here rather than inside the "pods to be checked" branch above, so
	// that a request served entirely from cache also carries its return code.
	types.SetSpanOK(span, statusCode)
}
