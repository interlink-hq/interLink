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

	types "github.com/intertwin-eu/interlink/pkg/interlink"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	trace "go.opentelemetry.io/otel/trace"
)

// Ping is just a very basic Ping function
func (h *InterLinkHandler) Ping(w http.ResponseWriter, _ *http.Request) {
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

	// ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	// defer cancel()
	respPlugin, err := http.DefaultClient.Do(req)
	//  respPlugin, err := DoReq(req.WithContext(ctx))
	// _, err = ReqWithError(h.Ctx, req, w, start, span, false, true, sessionContext, http.DefaultClient)
	if err != nil {
		log.G(h.Ctx).Error(err)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, err = w.Write([]byte(strconv.Itoa(http.StatusServiceUnavailable)))
		if err != nil {
			log.G(h.Ctx).Error(errors.New("Failed to write to http buffer"))
		}
		return
	}
	defer respPlugin.Body.Close()

	if respPlugin.StatusCode != http.StatusOK {
		log.G(h.Ctx).Error("error pinging plugin")
		w.WriteHeader(respPlugin.StatusCode)
		_, err = w.Write([]byte(strconv.Itoa(http.StatusServiceUnavailable)))
		if err != nil {
			log.G(h.Ctx).Error(errors.New("Failed to write to http buffer"))
		}

		return
	}

	types.SetDurationSpan(start, span, types.WithHTTPReturnCode(respPlugin.StatusCode))

	w.WriteHeader(http.StatusOK)
	_, err = w.Write([]byte("0"))
	if err != nil {
		log.G(h.Ctx).Error(errors.New("Failed to write to http buffer"))
	}
}
