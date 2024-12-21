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

	types "github.com/intertwin-eu/interlink/pkg/interlink"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	trace "go.opentelemetry.io/otel/trace"
)

func (h *InterLinkHandler) StatusHandler(w http.ResponseWriter, r *http.Request) {
	start := time.Now().UnixMicro()
	tracer := otel.Tracer("interlink-API")
	_, span := tracer.Start(h.Ctx, "StatusAPI", trace.WithAttributes(
		attribute.Int64("start.timestamp", start),
	))
	defer span.End()
	defer types.SetDurationSpan(start, span)
	statusCode := http.StatusOK
	var pods []*v1.Pod
	log.G(h.Ctx).Info("InterLink: received GetStatus call")

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		log.G(h.Ctx).Fatal(err)
	}

	err = json.Unmarshal(bodyBytes, &pods)
	if err != nil {
		errWithContext := fmt.Errorf("error doing fisrt Unmarshal() in StatusHandler() error detail: %s error: %w", fmt.Sprintf("%#v", err), err)
		log.G(h.Ctx).Error(errWithContext)
	}

	span.SetAttributes(
		attribute.Int("pods.count", len(pods)),
	)

	var podsToBeChecked []*v1.Pod
	var returnedStatuses []types.PodStatus // returned from the query to the sidecar
	var returnPods []types.PodStatus       // returned to the vk

	PodStatuses.mu.Lock()
	for _, pod := range pods {
		cached := checkIfCached(string(pod.UID))
		if pod.Status.Phase == v1.PodRunning || pod.Status.Phase == v1.PodPending || !cached {
			podsToBeChecked = append(podsToBeChecked, pod)
		}
		span.AddEvent("Pod "+pod.Name+" is cached", trace.WithAttributes(
			attribute.String("pod.name", pod.Name),
			attribute.String("pod.namespace", pod.Namespace),
			attribute.String("pod.uid", string(pod.UID)),
			attribute.String("pod.phase", string(pod.Status.Phase)),
		))
	}
	PodStatuses.mu.Unlock()

	if len(podsToBeChecked) > 0 {

		bodyBytes, err = json.Marshal(podsToBeChecked)
		if err != nil {
			log.G(h.Ctx).Fatal(err)
		}

		reader := bytes.NewReader(bodyBytes)
		req, err := http.NewRequest(http.MethodGet, h.SidecarEndpoint+"/status", reader)
		if err != nil {
			log.G(h.Ctx).Fatal(err)
		}

		log.G(h.Ctx).Info("InterLink: forwarding GetStatus call to sidecar")
		req.Header.Set("Content-Type", "application/json")
		log.G(h.Ctx).Debug("Interlink get status request content:", req)

		sessionContext := GetSessionContext(r)
		bodyBytes, err = ReqWithError(h.Ctx, req, w, start, span, false, true, sessionContext, *h.ClientHTTP)
		if err != nil {
			log.L.Error(err)
			return
		}

		err = json.Unmarshal(bodyBytes, &returnedStatuses)
		if err != nil {
			statusCode = http.StatusInternalServerError
			w.WriteHeader(statusCode)
			errWithContext := fmt.Errorf("error doing Unmarshal() in StatusHandler() of req %s error detail: %s error: %w", fmt.Sprintf("%#v", req), fmt.Sprintf("%#v", err), err)
			log.G(h.Ctx).Error(errWithContext)
			return
		}

		updateStatuses(returnedStatuses)
		types.SetDurationSpan(start, span, types.WithHTTPReturnCode(statusCode))

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
		for _, pod := range PodStatuses.Statuses {
			returnPods = append(returnPods, pod)
		}
	}

	returnValue, err := json.Marshal(returnPods)
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		log.G(h.Ctx).Error(err)
		return
	}
	log.G(h.Ctx).Debug("InterLink: status " + string(returnValue))

	w.WriteHeader(statusCode)
	_, err = w.Write(returnValue)
	if err != nil {
		log.G(h.Ctx).Error(errors.New("Failed to write to http buffer"))
	}

}
