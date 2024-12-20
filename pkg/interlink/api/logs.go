package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/containerd/containerd/log"

	types "github.com/intertwin-eu/interlink/pkg/interlink"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	trace "go.opentelemetry.io/otel/trace"
)

func (h *InterLinkHandler) GetLogsHandler(w http.ResponseWriter, r *http.Request) {
	start := time.Now().UnixMicro()
	tracer := otel.Tracer("interlink-API")
	_, span := tracer.Start(h.Ctx, "GetLogsAPI", trace.WithAttributes(
		attribute.Int64("start.timestamp", start),
	))
	defer span.End()
	defer types.SetDurationSpan(start, span)

	sessionContext := GetSessionContext(r)
	sessionContextMessage := GetSessionContextMessage(sessionContext)

	var statusCode int
	log.G(h.Ctx).Info(sessionContextMessage, "InterLink: received GetLogs call")
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		log.G(h.Ctx).Fatal(sessionContextMessage, err)
	}

	log.G(h.Ctx).Info(sessionContextMessage, "InterLink: unmarshal GetLogs request")
	var req2 types.LogStruct // incoming request. To be used in interlink API. req is directly forwarded to sidecar
	err = json.Unmarshal(bodyBytes, &req2)
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		log.G(h.Ctx).Error(sessionContextMessage, err)
		return
	}

	span.SetAttributes(
		attribute.String("pod.name", req2.PodName),
		attribute.String("pod.namespace", req2.Namespace),
		attribute.Int("opts.limitbytes", req2.Opts.LimitBytes),
		attribute.Int("opts.since", req2.Opts.SinceSeconds),
		attribute.Int64("opts.sincetime", req2.Opts.SinceTime.UnixMicro()),
		attribute.Int("opts.tail", req2.Opts.Tail),
		attribute.Bool("opts.follow", req2.Opts.Follow),
		attribute.Bool("opts.previous", req2.Opts.Previous),
		attribute.Bool("opts.timestamps", req2.Opts.Timestamps),
	)

	log.G(h.Ctx).Info(sessionContextMessage, "InterLink: new GetLogs podUID: now ", req2.PodUID)
	if (req2.Opts.Tail != 0 && req2.Opts.LimitBytes != 0) || (req2.Opts.SinceSeconds != 0 && !req2.Opts.SinceTime.IsZero()) {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)

		if req2.Opts.Tail != 0 && req2.Opts.LimitBytes != 0 {
			_, err = w.Write([]byte("Both Tail and LimitBytes set. Set only one of them"))
			if err != nil {
				log.G(h.Ctx).Error(errors.New(sessionContextMessage + "Failed to write to http buffer"))
			}
			return
		}

		_, err = w.Write([]byte("Both SinceSeconds and SinceTime set. Set only one of them"))
		if err != nil {
			log.G(h.Ctx).Error(errors.New(sessionContextMessage + "Failed to write to http buffer"))
		}

	}

	log.G(h.Ctx).Info(sessionContextMessage, "InterLink: marshal GetLogs request ")

	bodyBytes, err = json.Marshal(req2)
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		log.G(h.Ctx).Error(err)
		return
	}
	reader := bytes.NewReader(bodyBytes)
	req, err := http.NewRequest(http.MethodGet, h.SidecarEndpoint+"/getLogs", reader)
	if err != nil {
		log.G(h.Ctx).Fatal(err)
	}

	req.Header.Set("Content-Type", "application/json")

	// logTransport := http.DefaultTransport.(*http.Transport).Clone()
	// // logTransport.DisableKeepAlives = true
	// // logTransport.MaxIdleConnsPerHost = -1
	// var logHTTPClient = &http.Client{Transport: logTransport}

	log.G(h.Ctx).Info(sessionContextMessage, "InterLink: forwarding GetLogs call to sidecar")
	_, err = ReqWithError(h.Ctx, req, w, start, span, true, false, sessionContext)
	if err != nil {
		log.L.Error(sessionContextMessage, err)
		return
	}

}
