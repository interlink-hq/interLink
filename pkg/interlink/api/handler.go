// Package api provides HTTP handlers for the interLink API server.
// These handlers implement the core REST API endpoints that the Virtual Kubelet
// uses to communicate with interLink for pod lifecycle management.
package api

import (
	"bufio"
	"context"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/containerd/containerd/log"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"

	trace "go.opentelemetry.io/otel/trace"
	v1 "k8s.io/api/core/v1"

	"github.com/interlink-hq/interlink/pkg/interlink"
)

// isSafeURL validates that a URL uses only http, https, or http+unix schemes.
// It blocks non-http(s) schemes (e.g. file://, ftp://) to prevent unexpected
// protocol usage. Localhost, loopback addresses, and private IP ranges are
// intentionally allowed because the sidecar plugin routinely runs on the same
// host or on internal/private network addresses in HPC and cluster environments.
// These URLs originate from trusted operator configuration (config files),
// not from user-controlled input, so private IPs are valid.
func isSafeURL(rawurl string) bool {
	u, err := url.Parse(rawurl)
	if err != nil {
		return false
	}
	if u.Scheme == "http+unix" {
		return true
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	return true
}

const tracerName = "interlink-API"

type detailedTracingContextKey struct{}

// startAPITrace creates a server span from the incoming request's propagated
// trace context and adds low-cardinality HTTP metadata shared by every API call.
func (h *InterLinkHandler) startAPITrace(r *http.Request, name, route string, start int64) (context.Context, trace.Span, string) {
	ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
	ctx = context.WithValue(ctx, detailedTracingContextKey{}, h.Config.Tracing.Detailed)
	sessionContext := GetSessionContext(r)

	attrs := []attribute.KeyValue{
		attribute.Int64("start.timestamp", start),
		attribute.String("http.request.method", r.Method),
		attribute.String("http.route", route),
		attribute.String("url.path", r.URL.Path),
		attribute.String("network.protocol.version", strconv.Itoa(r.ProtoMajor)+"."+strconv.Itoa(r.ProtoMinor)),
		attribute.String("interlink.http.session_id", sessionContext),
		attribute.Bool("interlink.tracing.detailed", h.Config.Tracing.Detailed),
	}

	if host, port := splitHostPort(r.Host); host != "" {
		attrs = append(attrs, attribute.String("server.address", host))
		if port != 0 {
			attrs = append(attrs, attribute.Int("server.port", port))
		}
	}

	if h.Config.Tracing.Detailed {
		if clientAddress, clientPort := splitHostPort(r.RemoteAddr); clientAddress != "" {
			attrs = append(attrs, attribute.String("client.address", clientAddress))
			if clientPort != 0 {
				attrs = append(attrs, attribute.Int("client.port", clientPort))
			}
		}
		if userAgent := r.UserAgent(); userAgent != "" {
			attrs = append(attrs, attribute.String("user_agent.original", userAgent))
		}
		if contentType := r.Header.Get("Content-Type"); contentType != "" {
			attrs = append(attrs, attribute.String("http.request.header.content_type", contentType))
		}
	}

	spanCtx, span := otel.Tracer(tracerName).Start(ctx, name,
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(attrs...),
	)
	return spanCtx, span, sessionContext
}

func detailedTracingEnabled(ctx context.Context) bool {
	enabled, _ := ctx.Value(detailedTracingContextKey{}).(bool)
	return enabled
}

func startHTTPClientSpan(ctx context.Context, req *http.Request, sessionContext string, respondWithValues, respondWithReturn bool) (context.Context, trace.Span) {
	attrs := []attribute.KeyValue{
		attribute.String("http.request.method", req.Method),
		attribute.String("url.scheme", req.URL.Scheme),
		attribute.String("url.path", req.URL.Path),
		attribute.String("server.address", req.URL.Hostname()),
		attribute.String("interlink.http.session_id", sessionContext),
	}
	if port, err := strconv.Atoi(req.URL.Port()); err == nil {
		attrs = append(attrs, attribute.Int("server.port", port))
	}
	if req.ContentLength >= 0 {
		attrs = append(attrs, attribute.Int64("http.request.body.size", req.ContentLength))
	}
	if req.URL.Scheme == "http+unix" {
		attrs = append(attrs, attribute.String("network.transport", "unix"))
	}
	if detailedTracingEnabled(ctx) {
		attrs = append(attrs,
			attribute.Bool("interlink.http.respond_with_values", respondWithValues),
			attribute.Bool("interlink.http.respond_with_return", respondWithReturn),
		)
	}

	return otel.Tracer(tracerName).Start(ctx, "HTTP "+req.Method,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attrs...),
	)
}

func setRequestBodySize(span trace.Span, body []byte) {
	span.SetAttributes(attribute.Int("http.request.body.size", len(body)))
}

func setPodSpanAttributes(span trace.Span, pod *v1.Pod, detailed bool) {
	if pod == nil {
		return
	}

	span.SetAttributes(
		// Keep the original pod.* keys for existing queries and dashboards.
		attribute.String("pod.name", pod.Name),
		attribute.String("pod.namespace", pod.Namespace),
		attribute.String("pod.uid", string(pod.UID)),
		attribute.String("k8s.pod.name", pod.Name),
		attribute.String("k8s.namespace.name", pod.Namespace),
		attribute.String("k8s.pod.uid", string(pod.UID)),
		attribute.Int("interlink.pod.container.count", len(pod.Spec.Containers)),
		attribute.Int("interlink.pod.init_container.count", len(pod.Spec.InitContainers)),
		attribute.Int("interlink.pod.ephemeral_container.count", len(pod.Spec.EphemeralContainers)),
		attribute.Int("interlink.pod.volume.count", len(pod.Spec.Volumes)),
	)

	if !detailed {
		return
	}

	attrs := []attribute.KeyValue{
		attribute.StringSlice("interlink.pod.container.names", containerNames(pod.Spec.Containers)),
		attribute.StringSlice("interlink.pod.container.images", containerImages(pod.Spec.Containers)),
		attribute.StringSlice("interlink.pod.container.env_var.names", containerEnvNames(pod.Spec.Containers)),
		attribute.StringSlice("interlink.pod.container.volume_mounts", containerVolumeMounts(pod.Spec.Containers)),
		attribute.StringSlice("interlink.pod.container.resource.requests", containerResources(pod.Spec.Containers, true)),
		attribute.StringSlice("interlink.pod.container.resource.limits", containerResources(pod.Spec.Containers, false)),
		attribute.StringSlice("interlink.pod.init_container.names", containerNames(pod.Spec.InitContainers)),
		attribute.StringSlice("interlink.pod.init_container.images", containerImages(pod.Spec.InitContainers)),
		attribute.StringSlice("interlink.pod.init_container.env_var.names", containerEnvNames(pod.Spec.InitContainers)),
		attribute.StringSlice("interlink.pod.init_container.volume_mounts", containerVolumeMounts(pod.Spec.InitContainers)),
		attribute.StringSlice("interlink.pod.init_container.resource.requests", containerResources(pod.Spec.InitContainers, true)),
		attribute.StringSlice("interlink.pod.init_container.resource.limits", containerResources(pod.Spec.InitContainers, false)),
		attribute.StringSlice("interlink.pod.volume.names", volumeNames(pod.Spec.Volumes)),
		attribute.StringSlice("interlink.pod.volume.types", volumeTypes(pod.Spec.Volumes)),
		attribute.StringSlice("interlink.pod.label.keys", sortedMapKeys(pod.Labels)),
		attribute.StringSlice("interlink.pod.annotation.keys", sortedMapKeys(pod.Annotations)),
		attribute.String("interlink.pod.service_account.name", pod.Spec.ServiceAccountName),
		attribute.String("interlink.pod.restart_policy", string(pod.Spec.RestartPolicy)),
		attribute.String("interlink.pod.scheduler.name", pod.Spec.SchedulerName),
		attribute.String("interlink.pod.priority_class.name", pod.Spec.PriorityClassName),
		attribute.Bool("interlink.pod.host_network", pod.Spec.HostNetwork),
	}
	if pod.Spec.RuntimeClassName != nil {
		attrs = append(attrs, attribute.String("interlink.pod.runtime_class.name", *pod.Spec.RuntimeClassName))
	}
	span.SetAttributes(attrs...)
}

func setCreateRequestSpanAttributes(span trace.Span, pod interlink.PodCreateRequests, detailed bool) {
	setPodSpanAttributes(span, &pod.Pod, detailed)
	span.SetAttributes(
		attribute.Int("interlink.create.config_map.count", len(pod.ConfigMaps)),
		attribute.Int("interlink.create.secret.count", len(pod.Secrets)),
		attribute.Int("interlink.create.projected_volume.count", len(pod.ProjectedVolumeMaps)),
		attribute.Bool("interlink.create.job_script_builder.enabled", pod.JobScriptBuilderURL != ""),
	)

	if !detailed {
		return
	}
	span.SetAttributes(
		attribute.StringSlice("interlink.create.config_map.names", configMapNames(pod.ConfigMaps)),
		attribute.StringSlice("interlink.create.secret.names", secretNames(pod.Secrets)),
		attribute.StringSlice("interlink.create.projected_volume.names", configMapNames(pod.ProjectedVolumeMaps)),
	)
}

func splitHostPort(address string) (string, int) {
	if address == "" {
		return "", 0
	}
	host, rawPort, err := net.SplitHostPort(address)
	if err != nil {
		return strings.Trim(address, "[]"), 0
	}
	port, _ := strconv.Atoi(rawPort)
	return host, port
}

func containerNames(containers []v1.Container) []string {
	names := make([]string, 0, len(containers))
	for _, container := range containers {
		names = append(names, container.Name)
	}
	return names
}

func containerImages(containers []v1.Container) []string {
	images := make([]string, 0, len(containers))
	for _, container := range containers {
		images = append(images, container.Image)
	}
	return images
}

func containerEnvNames(containers []v1.Container) []string {
	names := make([]string, 0)
	for _, container := range containers {
		for _, env := range container.Env {
			names = append(names, container.Name+":"+env.Name)
		}
		for _, envFrom := range container.EnvFrom {
			switch {
			case envFrom.ConfigMapRef != nil:
				names = append(names, container.Name+":configmap:"+envFrom.ConfigMapRef.Name)
			case envFrom.SecretRef != nil:
				names = append(names, container.Name+":secret:"+envFrom.SecretRef.Name)
			}
		}
	}
	return names
}

func containerVolumeMounts(containers []v1.Container) []string {
	mounts := make([]string, 0)
	for _, container := range containers {
		for _, mount := range container.VolumeMounts {
			mounts = append(mounts, container.Name+":"+mount.Name)
		}
	}
	return mounts
}

func containerResources(containers []v1.Container, requests bool) []string {
	resources := make([]string, 0)
	for _, container := range containers {
		resourceList := container.Resources.Limits
		if requests {
			resourceList = container.Resources.Requests
		}
		resourceNames := make([]string, 0, len(resourceList))
		for name := range resourceList {
			resourceNames = append(resourceNames, string(name))
		}
		sort.Strings(resourceNames)
		for _, name := range resourceNames {
			quantity := resourceList[v1.ResourceName(name)]
			resources = append(resources, container.Name+":"+name+"="+quantity.String())
		}
	}
	return resources
}

func volumeNames(volumes []v1.Volume) []string {
	names := make([]string, 0, len(volumes))
	for _, volume := range volumes {
		names = append(names, volume.Name)
	}
	return names
}

func volumeTypes(volumes []v1.Volume) []string {
	types := make([]string, 0, len(volumes))
	for _, volume := range volumes {
		volumeType := "other"
		switch {
		case volume.ConfigMap != nil:
			volumeType = "config_map"
		case volume.Secret != nil:
			volumeType = "secret"
		case volume.Projected != nil:
			volumeType = "projected"
		case volume.EmptyDir != nil:
			volumeType = "empty_dir"
		case volume.PersistentVolumeClaim != nil:
			volumeType = "persistent_volume_claim"
		case volume.HostPath != nil:
			volumeType = "host_path"
		case volume.DownwardAPI != nil:
			volumeType = "downward_api"
		}
		types = append(types, volume.Name+":"+volumeType)
	}
	return types
}

func configMapNames(configMaps []v1.ConfigMap) []string {
	names := make([]string, 0, len(configMaps))
	for _, configMap := range configMaps {
		names = append(names, configMap.Name)
	}
	return names
}

func secretNames(secrets []v1.Secret) []string {
	names := make([]string, 0, len(secrets))
	for _, secret := range secrets {
		names = append(names, secret.Name)
	}
	return names
}

func sortedMapKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// InterLinkHandler handles HTTP requests for the interLink API server.
// It acts as a proxy between the Virtual Kubelet and sidecar plugins,
// forwarding requests and managing pod lifecycle operations.
type InterLinkHandler struct {
	// Config holds the interLink configuration
	Config interlink.Config
	// Ctx is the context for request processing
	Ctx context.Context
	// SidecarEndpoint is the URL of the sidecar plugin
	SidecarEndpoint string
	// ClientHTTP is the HTTP client for communicating with the sidecar
	ClientHTTP *http.Client
}

// AddSessionContext adds a session identifier to the HTTP request headers.
// This enables end-to-end tracing of requests from Virtual Kubelet through
// interLink API to the sidecar plugin.
func AddSessionContext(req *http.Request, sessionContext string) {
	req.Header.Set("InterLink-Http-Session", sessionContext)
}

// GetSessionContext retrieves or generates a session context identifier for request tracing.
// If no session context exists in the request headers, a new UUID-based identifier is generated.
// Returns the session context string for use in logging and tracing.
func GetSessionContext(r *http.Request) string {
	sessionContext := r.Header.Get("InterLink-Http-Session")
	if sessionContext == "" {
		// Generate a new session ID if none exists
		id := uuid.New()
		sessionContext = "Request-" + id.String()
	}
	return sessionContext
}

// GetSessionContextMessage formats a session context into a standardized log message prefix.
// This ensures consistent logging format across all HTTP operations.
func GetSessionContextMessage(sessionContext string) string {
	return "HTTP InterLink session " + sessionContext + ": "
}

// ReqWithError executes an HTTP request to a sidecar plugin and handles the response.
// This function provides comprehensive error handling, request tracing, and response streaming.
// It supports both buffered and streaming response modes for efficient handling of large responses.
//
// Parameters:
//   - ctx: Request context for cancellation and tracing
//   - req: HTTP request to execute
//   - w: Response writer to stream results to the client
//   - start: Start timestamp for performance measurement
//   - span: OpenTelemetry span for distributed tracing
//   - respondWithValues: If true, write response data to the ResponseWriter
//   - respondWithReturn: If true, return response data as bytes (use false for large responses)
//   - sessionContext: Session identifier for request tracing
//   - clientHTTP: HTTP client to use for the request
//
// Returns:
//   - []byte: Response body (only if respondWithReturn is true)
//   - error: Any error encountered during request processing
func ReqWithError(
	ctx context.Context,
	req *http.Request,
	w http.ResponseWriter,
	start int64,
	span trace.Span,
	respondWithValues bool,
	respondWithReturn bool,
	sessionContext string,
	clientHTTP *http.Client,
) (returnValue []byte, returnErr error) {
	log.G(ctx).Infof("[ReqWithError] Starting request to %s | respondWithValues=%v | respondWithReturn=%v | session=%s",
		req.URL.String(), respondWithValues, respondWithReturn, sessionContext)

	outboundCtx, clientSpan := startHTTPClientSpan(ctx, req, sessionContext, respondWithValues, respondWithReturn)
	defer clientSpan.End()
	clientStatusCode := 0
	defer func() {
		if returnErr != nil {
			interlink.SetSpanError(clientSpan, clientStatusCode, returnErr)
			return
		}
		interlink.SetSpanOK(clientSpan, clientStatusCode)
	}()
	req = req.Clone(outboundCtx)

	req.Header.Set("Content-Type", "application/json")

	sessionContextMessage := GetSessionContextMessage(sessionContext)
	log.G(ctx).Debug(sessionContextMessage, "doing request: ", fmt.Sprintf("%#v", req))

	// Add session number for end-to-end trace
	AddSessionContext(req, sessionContext)
	otel.GetTextMapPropagator().Inject(outboundCtx, propagation.HeaderCarrier(req.Header))

	if !isSafeURL(req.URL.String()) {
		statusCode := http.StatusInternalServerError
		clientStatusCode = statusCode
		errWithContext := fmt.Errorf("potential SSRF detected: %s", req.URL.String())
		w.WriteHeader(statusCode)
		interlink.SetSpanError(span, statusCode, errWithContext)
		return nil, errWithContext
	}
	resp, err := clientHTTP.Do(req) // #nosec G704
	if err != nil {
		statusCode := http.StatusInternalServerError
		clientStatusCode = statusCode
		log.G(ctx).Errorf("%s HTTP client.Do() failed: %v", sessionContextMessage, err)
		w.WriteHeader(statusCode)
		errWithContext := fmt.Errorf(sessionContextMessage+
			"error doing DoReq() of ReqWithErrorWithSessionNumber error %w", err)
		interlink.SetSpanError(span, statusCode, errWithContext)
		return nil, errWithContext
	}
	defer func() {
		log.G(ctx).Debugf("%s Closing response body", sessionContextMessage)
		resp.Body.Close()
	}()

	log.G(ctx).Infof("%s Received response: HTTP %d %s", sessionContextMessage, resp.StatusCode, http.StatusText(resp.StatusCode))
	clientStatusCode = resp.StatusCode
	clientSpan.SetAttributes(attribute.Int("http.response.status_code", resp.StatusCode))

	// Always write the response code to client
	log.G(ctx).Debugf("%s Writing response header: %d", sessionContextMessage, resp.StatusCode)
	w.WriteHeader(resp.StatusCode)

	// Record the sidecar's return code as soon as it is known, so that it is
	// present on the failure paths below too and not only on the success path.
	// This is the code the client actually observes: the WriteHeader calls that
	// follow are superfluous and ignored by net/http.
	interlink.SetDurationSpan(start, span, interlink.WithHTTPReturnCode(resp.StatusCode))

	// Flush headers immediately
	if f, ok := w.(http.Flusher); ok {
		log.G(ctx).Debug(sessionContextMessage, "Flushing headers to client...")
		f.Flush()
	} else {
		log.G(ctx).Warn(sessionContextMessage, "Server does not support Flusher.")
	}

	if resp.StatusCode != http.StatusOK {
		log.G(ctx).Errorf("%s Non-OK status from JobScriptBuilder: %d", sessionContextMessage, resp.StatusCode)
		statusCode := http.StatusInternalServerError

		// ❗This is likely the cause of “superfluous WriteHeader” (double call)
		// Add a debug to confirm
		log.G(ctx).Debugf("%s Writing error header: %d (may trigger 'superfluous WriteHeader')", sessionContextMessage, statusCode)
		w.WriteHeader(statusCode)

		ret, err := io.ReadAll(resp.Body)
		clientSpan.SetAttributes(attribute.Int("http.response.body.size", len(ret)))
		if err != nil {
			errWithContext := fmt.Errorf(sessionContextMessage+
				"HTTP request in error and could not read body response error: %w", err)
			interlink.SetSpanError(span, resp.StatusCode, errWithContext)
			return nil, errWithContext
		}

		errHTTPResponse := fmt.Errorf("%s call exit status: %d. Body: %s", sessionContextMessage, statusCode, ret)
		traceErr := fmt.Errorf("%s call exit status: %d", sessionContextMessage, resp.StatusCode)
		log.G(ctx).Error(errHTTPResponse)

		// Prevent XSS by escaping error message
		safeErr := html.EscapeString(errHTTPResponse.Error())
		_, err = w.Write([]byte(safeErr))
		if err != nil {
			errWithContext := fmt.Errorf(sessionContextMessage+
				"HTTP request in error and could not write all body response to InterLink Node error: %w", err)
			interlink.SetSpanError(span, resp.StatusCode, errWithContext)
			return nil, errWithContext
		}

		// Keep response payloads out of span status descriptions and exception
		// events. The full upstream response remains available in server logs.
		interlink.SetSpanError(span, resp.StatusCode, traceErr)
		return nil, traceErr
	}

	// ---------------------------
	// CASE: respondWithReturn == true
	// ---------------------------
	if respondWithReturn {
		log.G(ctx).Debug(sessionContextMessage, "RespondWithReturn mode: reading full body once")

		returnValue, err := io.ReadAll(resp.Body)
		clientSpan.SetAttributes(attribute.Int("http.response.body.size", len(returnValue)))
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			log.G(ctx).Errorf("%s Error reading response body: %v", sessionContextMessage, err)
			errWithContext := fmt.Errorf(sessionContextMessage+
				"error doing ReadAll() of ReqWithErrorComplex see error %w", err)
			interlink.SetSpanError(span, resp.StatusCode, errWithContext)
			return nil, errWithContext
		}

		log.G(ctx).Debugf("%s Response body (len=%d): %.500s", sessionContextMessage, len(returnValue), string(returnValue))

		if respondWithValues {
			log.G(ctx).Debug(sessionContextMessage, "Writing returnValue to client")
			_, err = w.Write(returnValue)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				log.G(ctx).Errorf("%s Error writing response body to client: %v", sessionContextMessage, err)
				errWithContext := fmt.Errorf(sessionContextMessage+
					"error doing Write() of ReqWithErrorComplex see error %w", err)
				interlink.SetSpanError(span, resp.StatusCode, errWithContext)
				return nil, errWithContext
			}
		}

		log.G(ctx).Infof("%s Completed request successfully (RespondWithReturn=true)", sessionContextMessage)
		return returnValue, nil
	}

	// ---------------------------
	// CASE: respondWithValues == true
	// ---------------------------
	if respondWithValues {
		log.G(ctx).Debug(sessionContextMessage, "RespondWithValues mode: streaming response")

		bodyReader := bufio.NewReader(resp.Body)
		bufferBytes := make([]byte, 4096)
		responseBytes := 0

		for {
			n, err := bodyReader.Read(bufferBytes)
			responseBytes += n
			if err != nil {
				if err == io.EOF {
					clientSpan.SetAttributes(attribute.Int("http.response.body.size", responseBytes))
					log.G(ctx).Debugf("%s EOF reached, read %d bytes", sessionContextMessage, n)
					if n > 0 {
						_, err = w.Write(bufferBytes[:n])
						if err != nil {
							w.WriteHeader(http.StatusInternalServerError)
							errWithContext := fmt.Errorf(sessionContextMessage+
								"could not write during ReqWithError() error: %w", err)
							interlink.SetSpanError(span, resp.StatusCode, errWithContext)
							return nil, errWithContext
						}
					}
					log.G(ctx).Infof("%s Completed request successfully (stream mode)", sessionContextMessage)
					return nil, nil
				}
				clientSpan.SetAttributes(attribute.Int("http.response.body.size", responseBytes))
				w.WriteHeader(http.StatusInternalServerError)
				log.G(ctx).Errorf("%s Error reading HTTP body: %v", sessionContextMessage, err)
				errWithContext := fmt.Errorf(sessionContextMessage+
					"could not read HTTP body: see error %w", err)
				interlink.SetSpanError(span, resp.StatusCode, errWithContext)
				return nil, errWithContext
			}

			log.G(ctx).Debugf("%s Read %d bytes from response", sessionContextMessage, n)
			_, err = w.Write(bufferBytes[:n])
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				log.G(ctx).Errorf("%s Error writing response chunk: %v", sessionContextMessage, err)
				errWithContext := fmt.Errorf(sessionContextMessage+
					"could not write during ReqWithError() error: %w", err)
				interlink.SetSpanError(span, resp.StatusCode, errWithContext)
				return nil, errWithContext
			}

			if f, ok := w.(http.Flusher); ok {
				f.Flush()
				log.G(ctx).Debug(sessionContextMessage, "Flushed response chunk to client")
			}
		}
	}

	// ---------------------------
	// CASE: no response needed
	// ---------------------------
	log.G(ctx).Infof("%s Completed request (no response mode)", sessionContextMessage)
	return nil, nil
}
