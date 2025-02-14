package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"text/template"
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
	defer types.SetInfoFromHeaders(span, &r.Header)

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

	data, err := getData(h.Ctx, h.Config, pod, span)
	if err != nil {
		statusCode = http.StatusInternalServerError
		log.G(h.Ctx).Error(err)
		w.WriteHeader(statusCode)
		return
	}

	if log.G(h.Ctx).Logger.IsLevelEnabled(log.DebugLevel) {
		// For debugging purpose only.
		allContainers := pod.Pod.Spec.InitContainers
		allContainers = append(allContainers, pod.Pod.Spec.Containers...)
		for _, container := range allContainers {
			for _, envVar := range container.Env {
				log.G(h.Ctx).Debug("InterLink VK environment variable to pod ", pod.Pod.Name, " container: ", container.Name, " env: ", envVar.Name, " value: ", envVar.Value)
			}
		}
	}

	// Here we fill the job.sh template is passed.
	switch {
	case pod.JobScriptBuilderURL != "":
		log.G(h.Ctx).Info("JobScriptBuilderURL: ", pod.JobScriptBuilderURL)
		if h.Config.JobScriptBuildConfig == nil {
			log.L.Error(fmt.Errorf("JobScript URL requested, but interlink does not have any Script build config set"))
			return
		}
		log.G(h.Ctx).Info("InterLink: asking JobScriptURL for job.sh")

		data.JobScriptBuild = *h.Config.JobScriptBuildConfig

		bodyBytes, err = json.Marshal(data)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			log.G(h.Ctx).Error(err)
			return
		}
		log.G(h.Ctx).Debug(string(bodyBytes))
		reader := bytes.NewReader(bodyBytes)
		req, err = http.NewRequest(http.MethodPost, pod.JobScriptBuilderURL, reader)
		if err != nil {
			log.L.Error(err)
			return
		}

		sessionContext := GetSessionContext(r)

		bodyBytesResp, err := ReqWithError(h.Ctx, req, w, start, span, true, false, sessionContext, http.DefaultClient)
		if err != nil {
			log.L.Error(err)
			return
		}

		data.JobScript = string(bodyBytesResp)

	case h.Config.JobScriptTemplate != "":

		tmp, err := template.ParseFiles(h.Config.JobScriptTemplate)

		if err != nil {
			statusCode = http.StatusInternalServerError
			log.G(h.Ctx).Error(err)
			w.WriteHeader(statusCode)
			return
		}

		var tpl bytes.Buffer
		err = tmp.Execute(&tpl, data)

		if err != nil {
			statusCode = http.StatusInternalServerError
			log.G(h.Ctx).Error(err)
			w.WriteHeader(statusCode)
			return
		}

		data.JobScript = tpl.String()
	}

	retrievedData = append(retrievedData, data)

	if retrievedData != nil {
		podIP, ok := retrievedData[0].Pod.Annotations["interlink.eu/pod-ip"]
		if ok {
			retrievedData[0].Pod.DeepCopy().Status.PodIP = podIP
		}

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
		_, err := ReqWithError(h.Ctx, req, w, start, span, true, false, sessionContext, h.ClientHTTP)
		if err != nil {
			log.L.Error(err)
			return
		}

	}
}
