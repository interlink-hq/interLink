package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"time"

	"github.com/containerd/containerd/log"

	types "github.com/interlink-hq/interlink/pkg/interlink"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	trace "go.opentelemetry.io/otel/trace"
)

// CreatePodSetsHandler handles HTTP POST requests to create the pods of one group in a
// single call, so that the sidecar plugin can run the whole group as one job.
// Every pod is prepared as CreateHandler prepares it (ConfigMaps, Secrets and projected
// volumes split by container, optional job script), and the group is forwarded to the
// plugin's /createPodSets. It is meant for plugins that list "podset-create" in their
// ping answer; status, logs and delete stay per pod.
//
// Request body: JSON-encoded PodSetsCreateRequests
// Response: JSON-encoded CreateStruct array, one entry per pod in the order of the
// request, all with the job ID of the group
//
// HTTP Status Codes:
//   - 200: Group creation request processed successfully
//   - 400: Bad request (malformed body, no pods, or a permanent refusal of the plugin)
//   - 503: The plugin cannot take the group now
//   - 500: Internal server error (job script or sidecar communication failures)
func (h *InterLinkHandler) CreatePodSetsHandler(w http.ResponseWriter, r *http.Request) {
	start := time.Now().UnixMicro()
	tracer := otel.Tracer("interlink-API")
	_, span := tracer.Start(h.Ctx, "CreatePodSetsAPI", trace.WithAttributes(
		attribute.Int64("start.timestamp", start),
	))
	defer span.End()
	defer types.SetDurationSpan(start, span)
	defer types.SetInfoFromHeaders(span, &r.Header)

	log.G(h.Ctx).Info("InterLink: received CreatePodSets call")

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.G(h.Ctx).Error(err)
		return
	}

	var request types.PodSetsCreateRequests
	if err := json.Unmarshal(bodyBytes, &request); err != nil {
		writePodSetsError(h.Ctx, w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}

	pods := 0
	for _, podSet := range request.PodSets {
		pods += len(podSet.Pods)
	}
	if pods == 0 {
		writePodSetsError(h.Ctx, w, http.StatusBadRequest, fmt.Errorf("group %s has no pods", request.GroupUID))
		return
	}

	span.SetAttributes(
		attribute.String("group.uid", request.GroupUID),
		attribute.Int("group.podsets", len(request.PodSets)),
		attribute.Int("group.pods", pods),
	)

	// One session for the whole group: GetSessionContext makes up a new one on every
	// call when the request has none.
	sessionContext := GetSessionContext(r)

	retrievedData := types.RetrievedPodSetsData{
		GroupUID: request.GroupUID,
		PodSets:  make([]types.RetrievedPodSetData, 0, len(request.PodSets)),
	}
	for _, podSet := range request.PodSets {
		retrievedPodSet := types.RetrievedPodSetData{
			Name: podSet.Name,
			Pods: make([]types.RetrievedPodData, 0, len(podSet.Pods)),
		}
		for _, pod := range podSet.Pods {
			log.G(h.Ctx).Infof("InterLink: preparing pod %s/%s of PodSet %s", pod.Pod.Namespace, pod.Pod.Name, podSet.Name)

			data, err := getData(h.Ctx, h.Config, pod, span)
			if err != nil {
				writePodSetsError(h.Ctx, w, http.StatusInternalServerError, fmt.Errorf("pod %s/%s: %w", pod.Pod.Namespace, pod.Pod.Name, err))
				return
			}

			statusCode, err := h.podSetJobScript(pod, &data, start, span, sessionContext)
			if err != nil {
				writePodSetsError(h.Ctx, w, statusCode, fmt.Errorf("pod %s/%s: job script: %w", pod.Pod.Namespace, pod.Pod.Name, err))
				return
			}

			retrievedPodSet.Pods = append(retrievedPodSet.Pods, data)
		}
		retrievedData.PodSets = append(retrievedData.PodSets, retrievedPodSet)
	}

	bodyBytes, err = json.Marshal(retrievedData)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.G(h.Ctx).Error(err)
		return
	}
	log.G(h.Ctx).Debug(string(bodyBytes))

	req, err := http.NewRequest(http.MethodPost, h.SidecarEndpoint+"/createPodSets", bytes.NewReader(bodyBytes))
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.G(h.Ctx).Error(err)
		return
	}

	log.G(h.Ctx).Infof("InterLink: forwarding CreatePodSets call to sidecar (group %s, %d pods)", request.GroupUID, pods)

	ForwardAccessToken(r, req)
	_, err = ReqWithError(h.Ctx, req, w, start, span, true, false, sessionContext, h.ClientHTTP)
	if err != nil {
		log.L.Error(err)
		return
	}
}

// podSetJobScript fills the job script of one pod of a group as CreateHandler does, from
// the JobScriptBuilderURL of the pod or from the configured JobScriptTemplate. Unlike
// CreateHandler it does not answer the caller: the group gets one answer, so it returns
// the status code to answer with when it fails.
func (h *InterLinkHandler) podSetJobScript(
	pod types.PodCreateRequests,
	data *types.RetrievedPodData,
	start int64,
	span trace.Span,
	sessionContext string,
) (int, error) {
	switch {
	case pod.JobScriptBuilderURL != "":
		log.G(h.Ctx).Info("JobScriptBuilderURL: ", pod.JobScriptBuilderURL)
		if h.Config.JobScriptBuildConfig == nil {
			return http.StatusInternalServerError, errors.New("JobScript URL requested, but interlink does not have any Script build config set")
		}

		data.JobScriptBuild = *h.Config.JobScriptBuildConfig

		bodyBytes, err := json.Marshal(data)
		if err != nil {
			return http.StatusInternalServerError, err
		}

		req, err := http.NewRequest(http.MethodPost, pod.JobScriptBuilderURL, bytes.NewReader(bodyBytes))
		if err != nil {
			return http.StatusInternalServerError, err
		}
		req.Header.Set("Content-Type", "application/json")

		// No ForwardAccessToken here, as in CreateHandler: JobScriptBuilderURL comes from
		// the pod spec and can point outside the deployment.
		// ReqWithError writes the builder's status code to its ResponseWriter, which must
		// not be the caller's: the caller is answered once for the whole group.
		var builderStatus statusRecorder
		script, err := ReqWithError(h.Ctx, req, &builderStatus, start, span, false, true, sessionContext, http.DefaultClient)
		if err != nil {
			statusCode := builderStatus.code
			if statusCode == 0 || statusCode == http.StatusOK {
				statusCode = http.StatusInternalServerError
			}
			return statusCode, err
		}

		data.JobScript = string(script)
		log.G(h.Ctx).Infof("Updated JobScript of pod %s successfully (len=%d)", data.Pod.Name, len(data.JobScript))

	case h.Config.JobScriptTemplate != "":
		script, err := renderJobScript(h.Config.JobScriptTemplate, *data)
		if err != nil {
			return http.StatusInternalServerError, err
		}
		data.JobScript = script
	}
	return http.StatusOK, nil
}

// writePodSetsError answers the caller with statusCode and the error text, escaped as
// ReqWithError escapes the errors of the sidecar.
func writePodSetsError(ctx context.Context, w http.ResponseWriter, statusCode int, err error) {
	log.G(ctx).Error(err)
	w.WriteHeader(statusCode)
	if _, werr := w.Write([]byte(html.EscapeString(err.Error()))); werr != nil {
		log.G(ctx).Error(werr)
	}
}

// statusRecorder is a ResponseWriter that keeps only the status code written to it, for
// calls whose answer must not reach the caller directly.
type statusRecorder struct {
	header http.Header
	code   int
}

func (s *statusRecorder) Header() http.Header {
	if s.header == nil {
		s.header = http.Header{}
	}
	return s.header
}

// WriteHeader keeps the first status code only, as net/http does.
func (s *statusRecorder) WriteHeader(statusCode int) {
	if s.code == 0 {
		s.code = statusCode
	}
}

func (s *statusRecorder) Write(p []byte) (int, error) {
	s.WriteHeader(http.StatusOK)
	return len(p), nil
}

// Flush makes statusRecorder an http.Flusher, which ReqWithError otherwise warns about.
func (s *statusRecorder) Flush() {}
