package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/containerd/containerd/log"
	types "github.com/intertwin-eu/interlink/pkg/interlink"
	v1 "k8s.io/api/core/v1"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	trace "go.opentelemetry.io/otel/trace"
)

// Ping is just a very basic Ping function
func (h *InterLinkHandler) Ping(w http.ResponseWriter, r *http.Request) {
	start := time.Now().UnixMicro()
	tracer := otel.Tracer("interlink-API")
	_, span := tracer.Start(h.Ctx, "PingAPI", trace.WithAttributes(
		attribute.Int64("start.timestamp", start),
	))
	defer span.End()
	defer types.SetDurationSpan(start, span)

	log.G(h.Ctx).Info("InterLink: received Ping call")

	podsToBeChecked := []*v1.Pod{}
	bodyBytes, err := json.Marshal(podsToBeChecked)
	if err != nil {
		log.G(h.Ctx).Error(err)
	}

	reader := bytes.NewReader(bodyBytes)
	req, err := http.NewRequest(http.MethodGet, h.SidecarEndpoint+"/status", reader)
	if err != nil {
		log.G(h.Ctx).Error(err)
	}

	log.G(h.Ctx).Info("InterLink: forwarding GetStatus call to sidecar")
	req.Header.Set("Content-Type", "application/json")
	log.G(h.Ctx).Debug(req)
	respPlugin, err := http.DefaultClient.Do(req)
	if err != nil {
		log.G(h.Ctx).Error(err)
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(strconv.Itoa(http.StatusServiceUnavailable)))
		return
	}

	if respPlugin != nil {
		if respPlugin.StatusCode != http.StatusOK {
			log.G(h.Ctx).Error("error pinging plugin")
			w.WriteHeader(respPlugin.StatusCode)
			w.Write([]byte(strconv.Itoa(http.StatusServiceUnavailable)))
			return
		}

		types.SetDurationSpan(start, span, types.WithHTTPReturnCode(respPlugin.StatusCode))

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("0"))
	}
}
