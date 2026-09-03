package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/containerd/containerd/log"
	v1 "k8s.io/api/core/v1"

	types "github.com/interlink-hq/interlink/pkg/interlink"

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
	defer types.SetInfoFromHeaders(span, &r.Header)

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

	sessionContext := GetSessionContext(req)
	_, err = ReqWithError(h.Ctx, req, w, start, span, true, false, sessionContext, h.ClientHTTP)
	if err != nil {
		// ReqWithError has already marked the span as failed and recorded the
		// code it sent to the client. The WriteHeader below is superfluous —
		// a header has necessarily been written already — so the span keeps the
		// code the client actually observed rather than this one.
		log.G(h.Ctx).Error(err)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, err = w.Write([]byte(strconv.Itoa(http.StatusServiceUnavailable)))
		if err != nil {
			log.G(h.Ctx).Error(errors.New("failed to write to http buffer"))
		}
		return
	}

	// The return code was already recorded by ReqWithError, so only the outcome
	// is set here.
	types.SetSpanOK(span, 0)
}
