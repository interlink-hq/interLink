package api

import (
	"bytes"
	"encoding/json"
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

// DeleteHandler deletes the cached status for the provided Pod and forwards the request to the sidecar
func (h *InterLinkHandler) DeleteHandler(w http.ResponseWriter, r *http.Request) {
	start := time.Now().UnixMicro()
	tracer := otel.Tracer("interlink-API")
	_, span := tracer.Start(h.Ctx, "DeleteAPI", trace.WithAttributes(
		attribute.Int64("start.timestamp", start),
	))
	defer span.End()
	defer types.SetDurationSpan(start, span)

	log.G(h.Ctx).Info("InterLink: received Delete call")

	bodyBytes, err := io.ReadAll(r.Body)
	var statusCode int

	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		log.G(h.Ctx).Fatal(err)
	}

	var req *http.Request
	var pod *v1.Pod
	reader := bytes.NewReader(bodyBytes)
	err = json.Unmarshal(bodyBytes, &pod)

	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		log.G(h.Ctx).Fatal(err)
	}

	span.SetAttributes(
		attribute.String("pod.name", pod.Name),
		attribute.String("pod.namespace", pod.Namespace),
		attribute.String("pod.uid", string(pod.UID)),
	)

	deleteCachedStatus(string(pod.UID))
	req, err = http.NewRequest(http.MethodPost, h.SidecarEndpoint+"/delete", reader)
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		log.G(h.Ctx).Error(err)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	log.G(h.Ctx).Info("InterLink: forwarding Delete call to sidecar")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		log.G(h.Ctx).Error(err)
		return
	}

	if resp != nil {
		returnValue, err := io.ReadAll(resp.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			if err != nil {
				log.G(h.Ctx).Error(err)
			}
			return
		}
		statusCode = resp.StatusCode

		if statusCode != http.StatusOK {
			w.WriteHeader(http.StatusInternalServerError)
		} else {
			w.WriteHeader(http.StatusOK)
		}
		log.G(h.Ctx).Debug("InterLink: " + string(returnValue))
		var returnJSON []types.PodStatus
		returnJSON = append(returnJSON, types.PodStatus{PodName: pod.Name, PodUID: string(pod.UID), PodNamespace: pod.Namespace})

		bodyBytes, err = json.Marshal(returnJSON)
		if err != nil {
			log.G(h.Ctx).Error(err)
			_, err = w.Write([]byte{})
			if err != nil {
				log.G(h.Ctx).Error(err)
			}
		} else {
			types.SetDurationSpan(start, span, types.WithHTTPReturnCode(statusCode))
			_, err = w.Write(bodyBytes)
			if err != nil {
				log.G(h.Ctx).Error(err)
			}
		}
	}
}
