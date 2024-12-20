package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/containerd/containerd/log"

	types "github.com/intertwin-eu/interlink/pkg/interlink"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	trace "go.opentelemetry.io/otel/trace"
)

// CreateHandler collects and rearranges all needed ConfigMaps/Secrets/EmptyDirs to ship them to the sidecar, then sends a response to the client
func (h *InterLinkHandler) CreateHandler(w http.ResponseWriter, r *http.Request) {
	start := time.Now().UnixMicro()
	tracer := otel.Tracer("interlink-API")
	_, span := tracer.Start(h.Ctx, "CreateAPI", trace.WithAttributes(
		attribute.Int64("start.timestamp", start),
	))
	defer span.End()
	defer types.SetDurationSpan(start, span)

	log.G(h.Ctx).Info("InterLink: received Create call")

	var statusCode int

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		log.G(h.Ctx).Error(err)
		return
	}

	var req *http.Request           // request to forward to sidecar
	var pod types.PodCreateRequests // request for interlink
	err = json.Unmarshal(bodyBytes, &pod)
	if err != nil {
		statusCode = http.StatusInternalServerError
		log.G(h.Ctx).Error(err)
		w.WriteHeader(statusCode)
		return
	}

	span.SetAttributes(
		attribute.String("pod.name", pod.Pod.Name),
		attribute.String("pod.namespace", pod.Pod.Namespace),
		attribute.String("pod.uid", string(pod.Pod.UID)),
	)

	var retrievedData []types.RetrievedPodData

	data := types.RetrievedPodData{}
	if h.Config.ExportPodData {
		data, err = getData(h.Ctx, h.Config, pod, span)
		if err != nil {
			statusCode = http.StatusInternalServerError
			log.G(h.Ctx).Error(err)
			w.WriteHeader(statusCode)
			return
		}
	}

	retrievedData = append(retrievedData, data)

	if retrievedData != nil {
		bodyBytes, err = json.Marshal(retrievedData)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			log.G(h.Ctx).Error(err)
			return
		}
		log.G(h.Ctx).Debug(string(bodyBytes))
		reader := bytes.NewReader(bodyBytes)

		log.G(h.Ctx).Info(req)
		req, err = http.NewRequest(http.MethodPost, h.SidecarEndpoint+"/create", reader)

		if err != nil {
			statusCode = http.StatusInternalServerError
			w.WriteHeader(statusCode)
			log.G(h.Ctx).Error(err)
			return
		}

		log.G(h.Ctx).Info("InterLink: forwarding Create call to sidecar")

		sessionContext := GetSessionContext(r)
		_, err := ReqWithError(h.Ctx, req, w, start, span, true, false, sessionContext, strings.ReplaceAll(h.SidecarEndpoint, "unix://", ""))
		if err != nil {
			log.L.Error(err)
			return
		}

	}
}
