package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"time"

	"github.com/containerd/containerd/log"

	types "github.com/interlink-hq/interlink/pkg/interlink"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	trace "go.opentelemetry.io/otel/trace"
)

// containerNameRegexp validates that a container name contains only safe characters.
// Kubernetes container names follow RFC 1123 subdomain rules: lowercase alphanumeric
// characters, '-', and must start and end with an alphanumeric character.
var containerNameRegexp = regexp.MustCompile(`^[a-z0-9][a-z0-9\-]*[a-z0-9]$|^[a-z0-9]$`)

// namespaceRegexp validates Kubernetes namespace names (RFC 1123 label).
var namespaceRegexp = regexp.MustCompile(`^[a-z0-9][a-z0-9\-]*[a-z0-9]$|^[a-z0-9]$`)

// podUIDRegexp validates Kubernetes pod UIDs (standard UUID format).
var podUIDRegexp = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// validateLogRequest checks that the log request fields are well-formed and
// safe to use in file path construction. This prevents path traversal attacks
// when the sidecar plugin builds file paths from these values.
func validateLogRequest(req types.LogStruct) error {
	if !namespaceRegexp.MatchString(req.Namespace) {
		return errors.New("invalid namespace: must be a valid DNS label")
	}
	if !podUIDRegexp.MatchString(req.PodUID) {
		return errors.New("invalid pod UID: must be a valid UUID")
	}
	if req.ContainerName != "" && !containerNameRegexp.MatchString(req.ContainerName) {
		return errors.New("invalid container name: must be a valid DNS label")
	}
	return nil
}

// GetLogsHandler handles HTTP GET requests to retrieve container logs.
// This endpoint streams container logs from the sidecar plugin to the client,
// supporting various log retrieval options such as tailing, following, and filtering.
//
// The handler validates log options to prevent conflicting parameters:
//   - Tail and LimitBytes cannot both be set
//   - SinceSeconds and SinceTime cannot both be set
//
// Request body: JSON-encoded LogStruct with container identification and log options
// Response: Streamed plain text log data
//
// HTTP Status Codes:
//   - 200: Log retrieval successful (may be empty if no logs available)
//   - 400: Bad request (invalid or conflicting parameters)
//   - 500: Internal server error (sidecar communication failures)
func (h *InterLinkHandler) GetLogsHandler(w http.ResponseWriter, r *http.Request) {
	start := time.Now().UnixMicro()
	tracer := otel.Tracer("interlink-API")
	_, span := tracer.Start(h.Ctx, "GetLogsAPI", trace.WithAttributes(
		attribute.Int64("start.timestamp", start),
	))
	defer span.End()
	defer types.SetDurationSpan(start, span)
	defer types.SetInfoFromHeaders(span, &r.Header)

	sessionContext := GetSessionContext(r)
	sessionContextMessage := GetSessionContextMessage(sessionContext)

	log.G(h.Ctx).Info(sessionContextMessage, "InterLink: received GetLogs call")
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		log.G(h.Ctx).Fatal(sessionContextMessage, err)
	}

	log.G(h.Ctx).Info(sessionContextMessage, "InterLink: unmarshal GetLogs request")
	var req2 types.LogStruct // incoming request. To be used in interlink API. req is directly forwarded to sidecar
	err = json.Unmarshal(bodyBytes, &req2)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
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

	if err := validateLogRequest(req2); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		if _, werr := w.Write([]byte(err.Error())); werr != nil {
			log.G(h.Ctx).Error(errors.New(sessionContextMessage + "Failed to write to http buffer"))
		}
		return
	}

	if req2.Opts.Tail != 0 && req2.Opts.LimitBytes != 0 {
		w.WriteHeader(http.StatusBadRequest)
		if _, werr := w.Write([]byte("Both Tail and LimitBytes set. Set only one of them")); werr != nil {
			log.G(h.Ctx).Error(errors.New(sessionContextMessage + "Failed to write to http buffer"))
		}
		return
	}

	if req2.Opts.SinceSeconds != 0 && !req2.Opts.SinceTime.IsZero() {
		w.WriteHeader(http.StatusBadRequest)
		if _, werr := w.Write([]byte("Both SinceSeconds and SinceTime set. Set only one of them")); werr != nil {
			log.G(h.Ctx).Error(errors.New(sessionContextMessage + "Failed to write to http buffer"))
		}
		return
	}

	log.G(h.Ctx).Info(sessionContextMessage, "InterLink: marshal GetLogs request ")

	bodyBytes, err = json.Marshal(req2)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.G(h.Ctx).Error(err)
		return
	}
	reader := bytes.NewReader(bodyBytes)
	log.G(h.Ctx).Info("Sending log request to: ", h.SidecarEndpoint)
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, h.SidecarEndpoint+"/getLogs", reader)
	if err != nil {
		log.G(h.Ctx).Fatal(err)
	}

	req.Header.Set("Content-Type", "application/json")

	log.G(h.Ctx).Info(sessionContextMessage, "InterLink: forwarding GetLogs call to sidecar")
	_, err = ReqWithError(h.Ctx, req, w, start, span, true, false, sessionContext, h.ClientHTTP)
	if err != nil {
		log.L.Error(sessionContextMessage, err)
		return
	}
}
