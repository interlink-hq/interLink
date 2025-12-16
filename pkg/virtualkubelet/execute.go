package virtualkubelet

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/containerd/containerd/log"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	trace "go.opentelemetry.io/otel/trace"
	authenticationv1 "k8s.io/api/authentication/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	types "github.com/interlink-hq/interlink/pkg/interlink"
	k8sTypes "k8s.io/apimachinery/pkg/types"
)

const (
	PodPhaseInitialize = "Initializing"
	PodPhaseCompleted  = "Completed"
)

func parseDisableOffloadContainers(pod *v1.Pod) map[string]bool {
	disabledContainers := make(map[string]bool)

	if pod.Annotations == nil {
		return disabledContainers
	}

	annotation, exists := pod.Annotations[annDisableOffloadContainers]
	if !exists || strings.TrimSpace(annotation) == "" {
		return disabledContainers
	}

	// Parse comma-separated list
	containerNames := strings.Split(annotation, ",")
	for _, name := range containerNames {
		name = strings.TrimSpace(name)
		if name != "" {
			disabledContainers[name] = true
		}
	}

	return disabledContainers
}

func parseDisableOffloadInitContainers(pod *v1.Pod) map[string]bool {
	disabledContainers := make(map[string]bool)

	if pod.Annotations == nil {
		return disabledContainers
	}

	annotation, exists := pod.Annotations[annDisableOffloadInitContainers]
	if !exists || strings.TrimSpace(annotation) == "" {
		return disabledContainers
	}

	// Parse comma-separated list
	containerNames := strings.Split(annotation, ",")
	for _, name := range containerNames {
		name = strings.TrimSpace(name)
		if name != "" {
			disabledContainers[name] = true
		}
	}

	return disabledContainers
}

func getOffloadContainers(pod *v1.Pod) []v1.Container {
	disabledContainers := parseDisableOffloadContainers(pod)

	var offloadContainers []v1.Container
	for _, container := range pod.Spec.Containers {
		if !disabledContainers[container.Name] {
			offloadContainers = append(offloadContainers, container)
		}
	}

	return offloadContainers
}

func getLocalContainers(pod *v1.Pod) []v1.Container {
	disabledContainers := parseDisableOffloadContainers(pod)

	var localContainers []v1.Container
	for _, container := range pod.Spec.Containers {
		if disabledContainers[container.Name] {
			localContainers = append(localContainers, container)
		}
	}

	return localContainers
}

func getOffloadInitContainers(pod *v1.Pod) []v1.Container {
	disabledContainers := parseDisableOffloadInitContainers(pod)

	var offloadContainers []v1.Container
	for _, container := range pod.Spec.InitContainers {
		if !disabledContainers[container.Name] {
			offloadContainers = append(offloadContainers, container)
		}
	}

	return offloadContainers
}

func getLocalInitContainers(pod *v1.Pod) []v1.Container {
	disabledContainers := parseDisableOffloadInitContainers(pod)

	var localContainers []v1.Container
	for _, container := range pod.Spec.InitContainers {
		if disabledContainers[container.Name] {
			localContainers = append(localContainers, container)
		}
	}

	return localContainers
}

func hasLocalContainers(pod *v1.Pod) bool {
	return len(getLocalContainers(pod)) > 0 || len(getLocalInitContainers(pod)) > 0
}

func extractVolumesForLocalContainers(pod *v1.Pod) []v1.Volume {
	localContainers := getLocalContainers(pod)
	localInitContainers := getLocalInitContainers(pod)

	if len(localContainers) == 0 && len(localInitContainers) == 0 {
		return nil
	}

	volumeNames := make(map[string]bool)

	for _, container := range localContainers {
		for _, vm := range container.VolumeMounts {
			volumeNames[vm.Name] = true
		}
	}

	for _, container := range localInitContainers {
		for _, vm := range container.VolumeMounts {
			volumeNames[vm.Name] = true
		}
	}

	var volumes []v1.Volume
	for _, vol := range pod.Spec.Volumes {
		if volumeNames[vol.Name] {
			volumes = append(volumes, vol)
		}
	}

	return volumes
}

func failedMount(ctx context.Context, failedAndWait *bool, name string, pod *v1.Pod, p *Provider, err error) error {
	*failedAndWait = true
	log.G(ctx).Warningf("Unable to find ConfigMap %s for pod %s. Waiting for it to be initialized. Error was: %v. Current phase: %s", name, pod.Name, err, pod.Status.Phase)
	if pod.Status.Phase != PodPhaseInitialize {
		pod.Status.Phase = PodPhaseInitialize
		err := p.UpdatePod(ctx, pod)
		if err != nil {
			return err
		}
	}
	return nil
}

func traceExecute(ctx context.Context, pod *v1.Pod, name string, startHTTPCall int64) *trace.Span {
	tracer := otel.Tracer("interlink-service")
	_, spanHTTP := tracer.Start(ctx, name, trace.WithAttributes(
		attribute.String("pod.name", pod.Name),
		attribute.String("pod.namespace", pod.Namespace),
		attribute.String("pod.uid", string(pod.UID)),
		attribute.Int64("start.timestamp", startHTTPCall),
	))
	defer spanHTTP.End()
	defer types.SetDurationSpan(startHTTPCall, spanHTTP)

	return &spanHTTP
}

// createTLSHTTPClient creates an HTTP client with TLS/mTLS configuration
func createTLSHTTPClient(ctx context.Context, tlsConfig TLSConfig) (*http.Client, error) {
	if !tlsConfig.Enabled {
		return http.DefaultClient, nil
	}

	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
	}

	// Load CA certificate if provided
	if tlsConfig.CACertFile != "" {
		caCert, err := os.ReadFile(tlsConfig.CACertFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA certificate file %s: %w", tlsConfig.CACertFile, err)
		}

		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA certificate from %s", tlsConfig.CACertFile)
		}
		transport.TLSClientConfig.RootCAs = caCertPool
		log.G(ctx).Info("Loaded CA certificate for TLS client from: ", tlsConfig.CACertFile)
	}

	// Load client certificate and key for mTLS if provided
	if tlsConfig.CertFile != "" && tlsConfig.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(tlsConfig.CertFile, tlsConfig.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load client certificate pair (%s, %s): %w", tlsConfig.CertFile, tlsConfig.KeyFile, err)
		}
		transport.TLSClientConfig.Certificates = []tls.Certificate{cert}
		log.G(ctx).Info("Loaded client certificate for mTLS from: ", tlsConfig.CertFile, " and ", tlsConfig.KeyFile)
	}

	return &http.Client{Transport: transport}, nil
}

func doRequestWithClient(req *http.Request, token string, httpClient *http.Client) (*http.Response, error) {
	if token != "" {
		req.Header.Add("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	return httpClient.Do(req)
}

func getSidecarEndpoint(ctx context.Context, interLinkURL string, interLinkPort string) string {
	interLinkEndpoint := ""
	log.G(ctx).Info("InterlingURL: ", interLinkURL)
	switch {
	case strings.HasPrefix(interLinkURL, "unix://"):
		interLinkEndpoint = "http://unix"
	case strings.HasPrefix(interLinkURL, "http://"):
		interLinkEndpoint = interLinkURL + ":" + interLinkPort
	case strings.HasPrefix(interLinkURL, "https://"):
		interLinkEndpoint = interLinkURL + ":" + interLinkPort
	default:
		log.G(ctx).Fatal("InterLinkURL URL should either start per unix:// or http(s)://")
	}
	return interLinkEndpoint
}

// PingInterLink pings the InterLink API and returns true if there's an answer. The second return value is given by the answer provided by the API.
// The third return value contains the response body from the ping call.
func PingInterLink(ctx context.Context, config Config) (bool, int, string, error) {
	tracer := otel.Tracer("interlink-service")
	interLinkEndpoint := getSidecarEndpoint(ctx, config.InterlinkURL, config.InterlinkPort)
	log.G(ctx).Info("Pinging: " + interLinkEndpoint + "/pinglink")
	retVal := -1
	req, err := http.NewRequest(http.MethodPost, interLinkEndpoint+"/pinglink", nil)
	if err != nil {
		log.G(ctx).Error(err)
	}

	if config.VKTokenFile != "" {
		token, err := os.ReadFile(config.VKTokenFile) // just pass the file name
		if err != nil {
			log.G(ctx).Error(err)
			return false, retVal, "", err
		}
		req.Header.Add("Authorization", "Bearer "+string(token))
	}

	startHTTPCall := time.Now().UnixMicro()
	_, spanHTTP := tracer.Start(ctx, "PingHttpCall", trace.WithAttributes(
		attribute.Int64("start.timestamp", startHTTPCall),
	))
	defer spanHTTP.End()
	defer types.SetDurationSpan(startHTTPCall, spanHTTP)

	// Add session number for end-to-end from VK to API to InterLink plugin (eg interlink-slurm-plugin)
	AddSessionContext(req, "PingInterLink#"+strconv.Itoa(rand.Intn(100000)))

	// Create TLS-enabled HTTP client
	httpClient, err := createTLSHTTPClient(ctx, config.TLS)
	if err != nil {
		log.G(ctx).Error("Failed to create TLS HTTP client: ", err)
		return false, retVal, "", err
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		spanHTTP.SetAttributes(attribute.Int("exit.code", http.StatusInternalServerError))
		return false, retVal, "", err
	}
	defer resp.Body.Close()

	types.SetDurationSpan(startHTTPCall, spanHTTP, types.WithHTTPReturnCode(resp.StatusCode))
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		log.G(ctx).Error(err)
		return false, retVal, "", err
	}

	if resp.StatusCode != http.StatusOK {
		log.G(ctx).Error("server error: " + fmt.Sprint(resp.StatusCode))
		return false, retVal, string(respBody), nil
	}

	return true, resp.StatusCode, string(respBody), nil
}

// updateCacheRequest is called when the VK receives the status of a pod already deleted. It performs a REST call InterLink API to update the cache deleting that pod from the cached structure
func updateCacheRequest(ctx context.Context, config Config, pod v1.Pod, token string) error {
	bodyBytes, err := json.Marshal(pod)
	if err != nil {
		log.L.Error(err)
		return err
	}

	interLinkEndpoint := getSidecarEndpoint(ctx, config.InterlinkURL, config.InterlinkPort)
	reader := bytes.NewReader(bodyBytes)
	req, err := http.NewRequest(http.MethodPost, interLinkEndpoint+"/updateCache", reader)
	if err != nil {
		log.L.Error(err)
		return err
	}

	if token != "" {
		req.Header.Add("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")

	startHTTPCall := time.Now().UnixMicro()
	spanHTTP := traceExecute(ctx, &pod, "UpdateCacheHttpCall", startHTTPCall)

	// Add session number for end-to-end from VK to API to InterLink plugin (eg interlink-slurm-plugin)
	AddSessionContext(req, "UpdateCache#"+strconv.Itoa(rand.Intn(100000)))

	// Create TLS-enabled HTTP client
	httpClient, err := createTLSHTTPClient(ctx, config.TLS)
	if err != nil {
		log.L.Error("Failed to create TLS HTTP client: ", err)
		return err
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		log.L.Error(err)
		return err
	}
	defer resp.Body.Close()

	types.SetDurationSpan(startHTTPCall, *spanHTTP, types.WithHTTPReturnCode(resp.StatusCode))
	if resp.StatusCode != http.StatusOK {
		return errors.New("Unexpected error occured while updating InterLink cache. Status code: " + strconv.Itoa(resp.StatusCode) + ". Check InterLink's logs for further informations")
	}

	return err
}

// createRequest performs a REST call to the InterLink API when a Pod is registered to the VK. It Marshals the pod with already retrieved ConfigMaps and Secrets and sends it to InterLink.
// Returns the call response expressed in bytes and/or the first encountered error
func createRequest(ctx context.Context, config Config, pod types.PodCreateRequests, token string) ([]byte, error) {
	tracer := otel.Tracer("interlink-service")
	interLinkEndpoint := getSidecarEndpoint(ctx, config.InterlinkURL, config.InterlinkPort)

	if config.JobScriptBuilderURL != "" {
		pod.JobScriptBuilderURL = config.JobScriptBuilderURL
	}

	bodyBytes, err := json.Marshal(pod)
	if err != nil {
		log.L.Error(err)
		return nil, err
	}
	reader := bytes.NewReader(bodyBytes)
	req, err := http.NewRequest(http.MethodPost, interLinkEndpoint+"/create", reader)
	if err != nil {
		log.L.Error(err)
		return nil, err
	}

	startHTTPCall := time.Now().UnixMicro()
	_, spanHTTP := tracer.Start(ctx, "CreateHttpCall", trace.WithAttributes(
		attribute.String("pod.name", pod.Pod.Name),
		attribute.String("pod.namespace", pod.Pod.Namespace),
		attribute.String("pod.uid", string(pod.Pod.UID)),
		attribute.Int64("start.timestamp", startHTTPCall),
	))
	defer spanHTTP.End()
	defer types.SetDurationSpan(startHTTPCall, spanHTTP)

	// Add session number for end-to-end from VK to API to InterLink plugin (eg interlink-slurm-plugin)
	AddSessionContext(req, "CreatePod#"+strconv.Itoa(rand.Intn(100000)))

	// Create TLS-enabled HTTP client
	httpClient, err := createTLSHTTPClient(ctx, config.TLS)
	if err != nil {
		return nil, fmt.Errorf("failed to create TLS HTTP client: %w", err)
	}

	resp, err := doRequestWithClient(req, token, httpClient)
	if err != nil {
		return nil, fmt.Errorf("error doing doRequest() in createRequest() log request: %s error: %w", fmt.Sprintf("%#v", req), err)
	}
	defer resp.Body.Close()

	types.SetDurationSpan(startHTTPCall, spanHTTP, types.WithHTTPReturnCode(resp.StatusCode))

	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("Unexpected error occured while creating Pods. Status code: " + strconv.Itoa(resp.StatusCode) + ". Check InterLink's logs for further informations")
	}
	returnValue, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error doing ReadAll() in createRequest() log request: %s error: %w", fmt.Sprintf("%#v", req), err)
	}

	return returnValue, nil
}

// deleteRequest performs a REST call to the InterLink API when a Pod is deleted from the VK. It Marshals the standard v1.Pod struct and sends it to InterLink.
// Returns the call response expressed in bytes and/or the first encountered error
func deleteRequest(ctx context.Context, config Config, pod *v1.Pod, token string) ([]byte, error) {
	interLinkEndpoint := getSidecarEndpoint(ctx, config.InterlinkURL, config.InterlinkPort)
	var returnValue []byte
	bodyBytes, err := json.Marshal(pod)
	if err != nil {
		log.G(context.Background()).Error(err)
		return nil, err
	}
	reader := bytes.NewReader(bodyBytes)
	req, err := http.NewRequest(http.MethodDelete, interLinkEndpoint+"/delete", reader)
	if err != nil {
		log.G(context.Background()).Error(err)
		return nil, err
	}

	startHTTPCall := time.Now().UnixMicro()
	spanHTTP := traceExecute(ctx, pod, "DeleteHttpCall", startHTTPCall)

	// Add session number for end-to-end from VK to API to InterLink plugin (eg interlink-slurm-plugin)
	AddSessionContext(req, "DeletePod#"+strconv.Itoa(rand.Intn(100000)))

	// Create TLS-enabled HTTP client
	httpClient, err := createTLSHTTPClient(ctx, config.TLS)
	if err != nil {
		log.G(context.Background()).Error("Failed to create TLS HTTP client: ", err)
		return nil, err
	}

	resp, err := doRequestWithClient(req, token, httpClient)
	if err != nil {
		log.G(context.Background()).Error(err)
		return nil, err
	}
	defer resp.Body.Close()

	statusCode := resp.StatusCode
	types.SetDurationSpan(startHTTPCall, *spanHTTP, types.WithHTTPReturnCode(resp.StatusCode))

	if statusCode != http.StatusOK {
		return nil, errors.New("Unexpected error occured while deleting Pods. Status code: " + strconv.Itoa(resp.StatusCode) + ". Check InterLink's logs for further informations")
	}

	returnValue, err = io.ReadAll(resp.Body)
	if err != nil {
		log.G(context.Background()).Error(err)
		return nil, err
	}
	log.G(context.Background()).Info(string(returnValue))

	return returnValue, nil
}

// statusRequest performs a REST call to the InterLink API when the VK needs an update on its Pods' status. A Marshalled slice of v1.Pod is sent to the InterLink API,
// to query the below plugin for their status.
// Returns the call response expressed in bytes and/or the first encountered error
func statusRequest(ctx context.Context, config Config, podsList []*v1.Pod, token string) ([]byte, error) {
	tracer := otel.Tracer("interlink-service")

	interLinkEndpoint := getSidecarEndpoint(ctx, config.InterlinkURL, config.InterlinkPort)

	bodyBytes, err := json.Marshal(podsList)
	if err != nil {
		log.L.Error(err)
		return nil, err
	}
	reader := bytes.NewReader(bodyBytes)
	req, err := http.NewRequest(http.MethodGet, interLinkEndpoint+"/status", reader)
	if err != nil {
		log.L.Error(err)
		return nil, err
	}

	//  log.L.Println(string(bodyBytes))

	startHTTPCall := time.Now().UnixMicro()
	_, spanHTTP := tracer.Start(ctx, "StatusHttpCall", trace.WithAttributes(
		attribute.Int64("start.timestamp", startHTTPCall),
	))
	defer spanHTTP.End()
	defer types.SetDurationSpan(startHTTPCall, spanHTTP)

	// Add session number for end-to-end from VK to API to InterLink plugin (eg interlink-slurm-plugin)
	AddSessionContext(req, "GetStatus#"+strconv.Itoa(rand.Intn(100000)))

	// Create TLS-enabled HTTP client
	httpClient, err := createTLSHTTPClient(ctx, config.TLS)
	if err != nil {
		return nil, fmt.Errorf("failed to create TLS HTTP client: %w", err)
	}

	resp, err := doRequestWithClient(req, token, httpClient)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	types.SetDurationSpan(startHTTPCall, spanHTTP, types.WithHTTPReturnCode(resp.StatusCode))
	if resp.StatusCode != http.StatusOK {
		returnValue, err := io.ReadAll(resp.Body)
		if err != nil {
			log.L.Error(err)
			return nil, err
		}
		return nil, errors.New("Unexpected error occured while getting status. Status code: " + strconv.Itoa(resp.StatusCode) + ". Check InterLink's logs for further informations\n" + string(returnValue))
	}
	returnValue, err := io.ReadAll(resp.Body)
	if err != nil {
		log.L.Error(err)
		return nil, err
	}

	return returnValue, nil
}

// LogRetrieval performs a REST call to the InterLink API when the user ask for a log retrieval. Compared to create/delete/status request, a way smaller struct is marshalled and sent.
// This struct only includes a minimum data set needed to identify the job/container to get the logs from.
// Returns the call response and/or the first encountered error
func LogRetrieval(
	ctx context.Context,
	config Config,
	logsRequest types.LogStruct,
	clientHTTPTransport *http.Transport,
	sessionContext string,
) (io.ReadCloser, error) {
	tracer := otel.Tracer("interlink-service")
	interLinkEndpoint := getSidecarEndpoint(ctx, config.InterlinkURL, config.InterlinkPort)

	token := ""

	if config.VKTokenFile != "" {
		b, err := os.ReadFile(config.VKTokenFile) // just pass the file name
		if err != nil {
			log.G(ctx).Fatal(err)
		}
		token = string(b)
	}

	sessionContextMessage := GetSessionContextMessage(sessionContext)

	bodyBytes, err := json.Marshal(logsRequest)
	if err != nil {
		errWithContext := fmt.Errorf(sessionContextMessage+"error during marshalling to JSON the log request: %s. Bodybytes: %s error: %w", fmt.Sprintf("%#v", logsRequest), bodyBytes, err)
		log.G(ctx).Error(errWithContext)
		return nil, errWithContext
	}

	reader := bytes.NewReader(bodyBytes)
	req, err := http.NewRequest(http.MethodGet, interLinkEndpoint+"/getLogs", reader)
	if err != nil {
		errWithContext := fmt.Errorf(sessionContextMessage+"error during HTTP request: %s/getLogs %w", interLinkEndpoint, err)
		log.G(ctx).Error(errWithContext)
		return nil, errWithContext
	}

	// log.G(ctx).Println(string(bodyBytes))

	startHTTPCall := time.Now().UnixMicro()
	_, spanHTTP := tracer.Start(ctx, "LogHttpCall", trace.WithAttributes(
		attribute.String("pod.name", logsRequest.PodName),
		attribute.String("pod.namespace", logsRequest.Namespace),
		attribute.String("pod.uid", logsRequest.PodUID),
		attribute.Int64("start.timestamp", startHTTPCall),
	))
	defer spanHTTP.End()
	defer types.SetDurationSpan(startHTTPCall, spanHTTP)

	log.G(ctx).Debug(sessionContextMessage, "before doRequestWithClient()")
	// Add session number for end-to-end from VK to API to InterLink plugin (eg interlink-slurm-plugin)
	AddSessionContext(req, sessionContext)

	clientHTTPTransport.DisableKeepAlives = true
	clientHTTPTransport.MaxIdleConnsPerHost = -1
	logHTTPClient := &http.Client{Transport: clientHTTPTransport}

	resp, err := doRequestWithClient(req, token, logHTTPClient)
	if err != nil {
		log.G(ctx).Error(err)
		return nil, err
	}
	// resp.body must not be closed because the kubelet needs to consume it! This is the responsability of the caller to close it.
	// Called here https://github.com/virtual-kubelet/virtual-kubelet/blob/v1.11.0/node/api/logs.go#L132
	// defer resp.Body.Close()
	log.G(ctx).Debug(sessionContextMessage, "after doRequestWithClient()")

	types.SetDurationSpan(startHTTPCall, spanHTTP, types.WithHTTPReturnCode(resp.StatusCode))
	if resp.StatusCode != http.StatusOK {
		err = errors.New(sessionContextMessage + "Unexpected error occured while getting logs. Status code: " + strconv.Itoa(resp.StatusCode) + ". Check InterLink's logs for further informations")
	}

	// return io.NopCloser(bufio.NewReader(resp.Body)), err
	return resp.Body, err
}

// Adds to pod environment variables related to services. For now, it only concerns Kubernetes API variables, example below:
/*
KUBERNETES_PORT=tcp://10.96.0.1:443
KUBERNETES_SERVICE_PORT=443
KUBERNETES_PORT_443_TCP_ADDR=10.96.0.1
KUBERNETES_PORT_443_TCP_PORT=443
KUBERNETES_PORT_443_TCP_PROTO=tcp
KUBERNETES_PORT_443_TCP=tcp://10.96.0.1:443
KUBERNETES_SERVICE_PORT_HTTPS=443
KUBERNETES_SERVICE_HOST=10.96.0.1
*/
func addKubernetesServicesEnvVars(ctx context.Context, config Config, pod *v1.Pod) {
	if config.KubernetesAPIAddr == "" || config.KubernetesAPIPort == "" {
		log.G(ctx).Info("InterLink configuration does not contains both KubernetesApiAddr and KubernetesApiPort, so no env var like KUBERNETES_SERVICE_HOST is added.")
		return
	}

	appendEnvVar := func(envs *[]v1.EnvVar, name string, value string) {
		envVar := v1.EnvVar{
			Name:  name,
			Value: value,
		}
		*envs = append(*envs, envVar)
	}
	appendEnvVars := func(containersPtr *[]v1.Container, index int) {
		containers := *containersPtr
		// container := containers[index]
		envsPtr := &containers[index].Env

		appendEnvVar(envsPtr, "KUBERNETES_PORT", "tcp://"+config.KubernetesAPIAddr+":"+config.KubernetesAPIPort)
		appendEnvVar(envsPtr, "KUBERNETES_SERVICE_PORT", config.KubernetesAPIPort)
		appendEnvVar(envsPtr, "KUBERNETES_PORT_443_TCP_ADDR", config.KubernetesAPIAddr)
		appendEnvVar(envsPtr, "KUBERNETES_PORT_443_TCP_PORT", config.KubernetesAPIPort)
		appendEnvVar(envsPtr, "KUBERNETES_PORT_443_TCP_PROTO", "tcp")
		appendEnvVar(envsPtr, "KUBERNETES_PORT_443_TCP", "tcp://"+config.KubernetesAPIAddr+":"+config.KubernetesAPIPort)
		appendEnvVar(envsPtr, "KUBERNETES_SERVICE_PORT_HTTPS", config.KubernetesAPIPort)
		appendEnvVar(envsPtr, "KUBERNETES_SERVICE_HOST", config.KubernetesAPIAddr)
	}
	// Warning: loop range copy value, so to modify original containers, we must use index instead.
	for i := range pod.Spec.InitContainers {
		appendEnvVars(&pod.Spec.InitContainers, i)
	}
	for i := range pod.Spec.Containers {
		appendEnvVars(&pod.Spec.Containers, i)
	}

	if log.G(ctx).Logger.IsLevelEnabled(log.DebugLevel) {
		// For debugging purpose only.
		for _, container := range pod.Spec.InitContainers {
			for _, envVar := range container.Env {
				log.G(ctx).Debug("in addKubernetesServicesEnvVars InterLink VK environment variable to pod ", pod.Name, " container: ", container.Name, " env: ", envVar.Name, " value: ", envVar.Value)
			}
		}
		for _, container := range pod.Spec.Containers {
			for _, envVar := range container.Env {
				log.G(ctx).Debug("in addKubernetesServicesEnvVars InterLink VK environment variable to pod ", pod.Name, " container: ", container.Name, " env: ", envVar.Name, " value: ", envVar.Value)
			}
		}
	}
	log.G(ctx).Info("InterLink VK added a set of environment variables (e.g.: KUBERNETES_SERVICE_HOST) to all containers of pod ",
		pod.Name, " k8s addr ", config.KubernetesAPIAddr, " k8s port ", config.KubernetesAPIPort)
}

// Handle projected sources and fills the projectedVolume object.
func remoteExecutionHandleProjectedSource(
	ctx context.Context, p *Provider, pod *v1.Pod, source v1.VolumeProjection, projectedVolume *v1.ConfigMap,
) error {
	switch {
	case source.ServiceAccountToken != nil:
		/* Case
		   - serviceAccountToken:
		       expirationSeconds: 3600
		       path: token
		*/
		log.G(ctx).Debug("Volume is a projected volume typed serviceAccountToken")

		// Now using TokenRequest API (https://kubernetes.io/docs/reference/kubernetes-api/authentication-resources/token-request-v1/)
		var expirationSeconds int64
		/*
			TODO: honor the expirationSeconds field and implement a rotation.
			if source.ServiceAccountToken.ExpirationSeconds != nil {
				expirationSeconds = *source.ServiceAccountToken.ExpirationSeconds
			} else {
				// If not expiration is set, set to 1h.
				expirationSeconds = 3600
			}
		*/
		// Infinite = 100 years
		expirationSeconds = 100 * 365 * 24 * 3600

		// Bount it to POD, so that token is deleted if pod is deleted. This is important given the illimited expiration.
		bountObjectRef := &authenticationv1.BoundObjectReference{
			Kind: "Pod",
			// Only one of UID or Name is sufficient, k8s will retrieve the other value.
			UID:  pod.UID,
			Name: pod.Name,
		}
		tokenRequest := &authenticationv1.TokenRequest{
			Spec: authenticationv1.TokenRequestSpec{
				// No need to set audience field. If set with wrong value, it might break token validity!
				ExpirationSeconds: &expirationSeconds,
				BoundObjectRef:    bountObjectRef,
			},
		}

		tokenRequestResult, err := p.clientSet.CoreV1().ServiceAccounts(pod.Namespace).CreateToken(
			ctx, pod.Spec.ServiceAccountName, tokenRequest, metav1.CreateOptions{})
		if err != nil {
			log.G(ctx).Error("error during token request in RemoteExecution() ", err)
		}
		log.G(ctx).Debug("could get token ", tokenRequestResult.Status.Token)

		// Add found token to result.
		projectedVolume.Data[source.ServiceAccountToken.Path] = tokenRequestResult.Status.Token

	case source.ConfigMap != nil:
		/* Case
		   - configMap:
		       items:
		         - key: ca.crt
		           path: ca.crt
		       name: kube-root-ca.crt
		*/
		for _, item := range source.ConfigMap.Items {
			const kubeCaCrt = "kube-root-ca.crt"
			overrideCaCrt := p.config.KubernetesAPICaCrt
			if source.ConfigMap.Name == kubeCaCrt && overrideCaCrt != "" {
				log.G(ctx).Debug("handling special case of Kubernetes API kube-root-ca.crt, override found, using provided ca.crt:, ", overrideCaCrt)
				projectedVolume.Data[item.Path] = overrideCaCrt
			} else {
				// This gets the usual certificate for K8s API, but it is restricted to whatever usual IP/FQDN of K8S API URL.
				// With InterLink, the Kubernetes internal network is not accessible so this default ca.crt is probably useless.
				log.G(ctx).Warning("using default Kubernetes API kube-root-ca.crt (no override found), but the default one might not be compatible with the subject: ", p.config.KubernetesAPIAddr)
				cfgmap, err := p.clientSet.CoreV1().ConfigMaps(pod.Namespace).Get(ctx, source.ConfigMap.Name, metav1.GetOptions{})
				if err != nil {
					return fmt.Errorf("error during retrieval of ConfigMap %s error: %w", source.ConfigMap.Name, err)
				}
				if value, ok := cfgmap.Data[item.Key]; ok {
					projectedVolume.Data[item.Path] = value
				} else {
					return fmt.Errorf("error during retrieval of key %s of (existing) ConfigMap %s error: %w", item.Key, source.ConfigMap.Name, err)
				}
			}
		}

	case source.DownwardAPI != nil:
		/* Case
		- downwardAPI:
			items:
			- fieldRef:
				apiVersion: v1
				fieldPath: metadata.namespace
				path: namespace
		*/
		// https://kubernetes.io/docs/concepts/workloads/pods/downward-api/
		// See URL doc above, that describe what type of DownwardAPI to expect from volume. For now, only FieldRef is supported.
		// The rest are ignored.
		for _, item := range source.DownwardAPI.Items {
			switch {

			case item.FieldRef != nil:
				switch item.FieldRef.FieldPath {
				case "metadata.name":
					projectedVolume.Data[item.Path] = pod.Name

				case "metadata.namespace":
					projectedVolume.Data[item.Path] = pod.Namespace

				case "metadata.uid":
					projectedVolume.Data[item.Path] = string(pod.UID)

				// TODO implement DownwardAPI annotation and label if needed.

				default:
					log.G(ctx).Warningf("in pod %s unsupported DownwardAPI FieldPath %s in InterLink, ignoring this source...", pod.Name, item.FieldRef.FieldPath)
				}

			case item.ResourceFieldRef != nil:
				// TODO implement DownwardAPI resourceFieldRef if needed.
				log.G(ctx).Warningf("in pod %s unsupported DownwardAPI resourceFieldRef in InterLink, ignoring this source...", pod.Name)

			default:
				log.G(ctx).Warningf("in pod %s unsupported unknown DownwardAPI in InterLink, ignoring this source...", pod.Name)
			}
		}
	}
	return nil
}

func remoteExecutionHandleVolumes(ctx context.Context, p *Provider, pod *v1.Pod, req *types.PodCreateRequests) error {
	startTime := time.Now()
	endTime := startTime.Add(5 * time.Minute)

	_, err := p.clientSet.CoreV1().Pods(pod.Namespace).Get(ctx, pod.Name, metav1.GetOptions{})
	if err != nil {
		log.G(ctx).Warning("Deleted Pod before actual creation")
		return nil
	}
	// Sometime the get secret or configmap can fail because it didn't have time to initialize, thus this
	// is not a true failure. We use this flag to wait.
	var failedAndWait bool

	log.G(ctx).Debug("Looking at volumes")
	for _, volume := range pod.Spec.Volumes {
		log.G(ctx).Debug("Looking at volume ", volume)
		for {
			failedAndWait = false
			if time.Now().Before(endTime) {
				switch {
				case volume.ConfigMap != nil:
					cfgmap, err := p.clientSet.CoreV1().ConfigMaps(pod.Namespace).Get(ctx, volume.ConfigMap.Name, metav1.GetOptions{})
					if err != nil {
						err = failedMount(ctx, &failedAndWait, volume.ConfigMap.Name, pod, p, err)
						if err != nil {
							return err
						}
					} else {
						req.ConfigMaps = append(req.ConfigMaps, *cfgmap)
					}

				case volume.Projected != nil:
					// The service account token uses the projected volume in K8S >= 1.24.

					if p.config.DisableProjectedVolumes {
						// This flag disable doing anything about Projected Volumes.
						log.G(ctx).Warning("Flag DisableProjectedVolumes set to true, so not handing Projected Volume: ", volume)
						break
					}

					var projectedVolume v1.ConfigMap
					projectedVolume.Name = volume.Name
					projectedVolume.Data = make(map[string]string)
					log.G(ctx).Debug("Adding to PodCreateRequests the projected volume ", volume.Name)
					req.ProjectedVolumeMaps = append(req.ProjectedVolumeMaps, projectedVolume)

					for _, source := range volume.Projected.Sources {
						err := remoteExecutionHandleProjectedSource(ctx, p, pod, source, &projectedVolume)
						if err != nil {
							return err
						}
						failedAndWait = false
						log.G(ctx).Debug("ProjectedVolumeMaps len: ", len(req.ProjectedVolumeMaps))
					}

				case volume.Secret != nil:
					scrt, err := p.clientSet.CoreV1().Secrets(pod.Namespace).Get(ctx, volume.Secret.SecretName, metav1.GetOptions{})
					if err != nil {
						err = failedMount(ctx, &failedAndWait, volume.Secret.SecretName, pod, p, err)
						if err != nil {
							return err
						}
					} else {
						req.Secrets = append(req.Secrets, *scrt)
					}

				case volume.EmptyDir != nil:
					log.G(ctx).Debugf("empty dir found, nothing to do for volume %s for Pod %s", volume.Name, pod.Name)

				default:
					log.G(ctx).Warningf("ignoring unsupported volume %s for Pod %s", volume.Name, pod.Name)
				}

				if failedAndWait {
					log.G(ctx).Warningf("volume %s not ready, sleeping 2s, attempt %f / 5 minutes max", volume.Name, time.Since(startTime).Minutes())
					time.Sleep(2 * time.Second)
					continue
				}
				pod.Status.Phase = v1.PodPending
				err = p.UpdatePod(ctx, pod)
				if err != nil {
					return err
				}
				break
			}

			pod.Status.Phase = v1.PodFailed
			pod.Status.Reason = "CFGMaps/Secrets not found"
			for i := range pod.Status.ContainerStatuses {
				pod.Status.ContainerStatuses[i].Ready = false
			}
			err = p.UpdatePod(ctx, pod)
			if err != nil {
				return err
			}
			return errors.New("unable to retrieve ConfigMaps or Secrets after 5m. Check logs")
		}
	}
	return nil
}

func resolveEnvRefs(
	ctx context.Context,
	p *Provider,
	pod *v1.Pod,
	container *v1.Container,
) {
	var annotationFieldRefRE = regexp.MustCompile(`^metadata\.annotations\[['"]?(.+?)['"]?\]$`)

	for i, env := range container.Env {
		if env.ValueFrom == nil {
			continue
		}

		if fr := env.ValueFrom.FieldRef; fr != nil {

			var resolved string
			switch fr.FieldPath {
			case "status.podIP":
				resolved = pod.Status.PodIP
			case "metadata.name":
				resolved = pod.Name
			case "metadata.namespace":
				resolved = pod.Namespace
			case "metadata.uid":
				resolved = string(pod.UID)

			default:
				if matches := annotationFieldRefRE.FindStringSubmatch(fr.FieldPath); len(matches) == 2 {
					annKey := matches[1]
					val, ok := pod.Annotations[annKey]
					if !ok {
						continue
					}
					resolved = val
				}
			}

			container.Env[i].Value = resolved
			container.Env[i].ValueFrom = nil
			continue

		}

		if sk := env.ValueFrom.SecretKeyRef; sk != nil {
			secret, err := p.clientSet.CoreV1().
				Secrets(pod.Namespace).
				Get(ctx, sk.Name, metav1.GetOptions{})
			if err != nil {
				log.G(ctx).Errorf("resolving Secret %s/%s: %v", pod.Namespace, sk.Name, err)
				continue
			}
			if data, ok := secret.Data[sk.Key]; ok {
				container.Env[i].Value = string(data)
				container.Env[i].ValueFrom = nil
			} else {
				log.G(ctx).Errorf("secret %s missing key %q", sk.Name, sk.Key)
			}
			continue
		}

		if cmr := env.ValueFrom.ConfigMapKeyRef; cmr != nil {
			cm, err := p.clientSet.CoreV1().
				ConfigMaps(pod.Namespace).
				Get(ctx, cmr.Name, metav1.GetOptions{})
			if err != nil {
				log.G(ctx).Errorf("resolving ConfigMap %s/%s: %v", pod.Namespace, cmr.Name, err)
				continue
			}
			if data, ok := cm.Data[cmr.Key]; ok {
				container.Env[i].Value = data
				container.Env[i].ValueFrom = nil
			} else {
				log.G(ctx).Errorf("configmap %s missing key %q", cmr.Name, cmr.Key)
			}
			continue
		}

		if rfr := env.ValueFrom.ResourceFieldRef; rfr != nil {
			targetName := rfr.ContainerName
			if targetName == "" {
				targetName = container.Name
			}
			var target *v1.Container
			for idx := range pod.Spec.Containers {
				if pod.Spec.Containers[idx].Name == targetName {
					target = &pod.Spec.Containers[idx]
					break
				}
			}
			if target == nil {
				log.G(ctx).Errorf("resourceFieldRef: container %q not found", targetName)
				continue
			}

			parts := strings.Split(rfr.Resource, ".")
			if len(parts) != 2 {
				log.G(ctx).Errorf("unsupported ResourceFieldRef %q", rfr.Resource)
				continue
			}

			var qty resource.Quantity
			switch parts[0] {
			case "requests":
				qty = target.Resources.Requests[v1.ResourceName(parts[1])]
			case "limits":
				qty = target.Resources.Limits[v1.ResourceName(parts[1])]
			default:
				log.G(ctx).Errorf("unsupported ResourceFieldRef scope %q", parts[0])
				continue
			}

			container.Env[i].Value = qty.String()
			container.Env[i].ValueFrom = nil
			continue
		}

		log.G(ctx).Warnf("env var %q has unhandled ValueFrom, skipping", env.Name)
	}
}

// RemoteExecution is called by the VK everytime a Pod is being registered or deleted to/from the VK.
// Depending on the mode (CREATE/DELETE), it performs different actions, making different REST calls.
// Note: for the CREATE mode, the function gets stuck up to 5 minutes waiting for every missing ConfigMap/Secret.
// If after 5m they are not still available, the function errors out
func RemoteExecution(ctx context.Context, config Config, p *Provider, pod *v1.Pod, mode int8) error {
	token := ""
	if config.VKTokenFile != "" {
		b, err := os.ReadFile(config.VKTokenFile) // just pass the file name
		if err != nil {
			log.G(ctx).Fatal(err)
			return err
		}
		token = string(b)
	}

	podToOffload := pod.DeepCopy()
	podToOffload.Spec.Containers = getOffloadContainers(pod)
	podToOffload.Spec.InitContainers = getOffloadInitContainers(pod)

	if len(podToOffload.Spec.Containers) > 0 {
		var offloadNames []string
		for _, c := range podToOffload.Spec.Containers {
			offloadNames = append(offloadNames, c.Name)
		}
		log.G(ctx).Infof("RemoteExecution: Offloading containers: %v", offloadNames)
	}
	if len(podToOffload.Spec.InitContainers) > 0 {
		var offloadInitNames []string
		for _, c := range podToOffload.Spec.InitContainers {
			offloadInitNames = append(offloadInitNames, c.Name)
		}
		log.G(ctx).Infof("RemoteExecution: Offloading init containers: %v", offloadInitNames)
	}

	switch mode {
	case CREATE:
		var req types.PodCreateRequests
		var resp types.CreateStruct

		req.Pod = *podToOffload

		err := remoteExecutionHandleVolumes(ctx, p, podToOffload, &req)
		if err != nil {
			return err
		}

		addKubernetesServicesEnvVars(ctx, config, podToOffload)

		if config.SkipDownwardAPIResolution {
			log.G(ctx).Info("SkipDownwardAPIResolution is set to true")
			for i := range podToOffload.Spec.InitContainers {
				resolveEnvRefs(ctx, p, podToOffload, &podToOffload.Spec.InitContainers[i])
			}
			for i := range podToOffload.Spec.Containers {
				resolveEnvRefs(ctx, p, podToOffload, &podToOffload.Spec.Containers[i])
			}
		}

		// For debugging purpose only.
		for _, container := range podToOffload.Spec.InitContainers {
			for _, envVar := range container.Env {
				log.G(ctx).Debug("InterLink VK environment variable to pod ", podToOffload.Name, " container: ", container.Name, " env: ", envVar.Name, " value: ", envVar.Value)
			}
		}
		for _, container := range podToOffload.Spec.Containers {
			for _, envVar := range container.Env {
				log.G(ctx).Debug("InterLink VK environment variable to pod ", podToOffload.Name, " container: ", container.Name, " env: ", envVar.Name, " value: ", envVar.Value)
			}
		}

		returnVal, err := createRequest(ctx, config, req, token)
		if err != nil {
			return fmt.Errorf("error doing createRequest() in RemoteExecution() return value %s error detail %s error: %w", returnVal, fmt.Sprintf("%#v", err), err)
		}

		log.G(ctx).Debug("Pod ", pod.Name, " with Job ID ", resp.PodJID, " before json.Unmarshal()")
		err = json.Unmarshal(returnVal, &resp)
		if err != nil {
			return fmt.Errorf("error doing Unmarshal() in RemoteExecution() return value %s error detail %s error: %w", returnVal, fmt.Sprintf("%#v", err), err)
		}

		if string(pod.UID) == resp.PodUID {
			if pod.Annotations == nil {
				pod.Annotations = map[string]string{}
			}
			pod.Annotations["JobID"] = resp.PodJID
		}

		err = p.UpdatePod(ctx, pod)
		if err != nil {
			return err
		}

		log.G(ctx).Info("Pod " + pod.Name + " created successfully and with Job ID " + resp.PodJID)
		log.G(ctx).Debug(string(returnVal))

	case DELETE:
		req := pod
		if pod.Status.Phase != PodPhaseInitialize {
			returnVal, err := deleteRequest(ctx, config, req, token)
			if err != nil {
				return err
			}
			log.G(ctx).Info(string(returnVal))
		}
	}
	return nil
}
func handleInitContainersUpdate(ctx context.Context, podRemoteStatus types.PodStatus, podRefInCluster *v1.Pod, nInitContainersInPod int) (bool, bool, bool, string, int) {
	log.G(ctx).Debug("Init containers detected, going to check them first")

	counterOfTerminatedInitContainers := 0
	podErrored := false
	failedReason := ""
	podWaitingForInitContainers := false
	podInit := false

	for _, containerRemoteStatus := range podRemoteStatus.InitContainers {
		index := 0
		foundCt := false

		for i, checkedContainer := range podRefInCluster.Status.InitContainerStatuses {
			if checkedContainer.Name == containerRemoteStatus.Name {
				foundCt = true
				index = i
				break
			}
		}

		if !foundCt {
			podRefInCluster.Status.InitContainerStatuses = append(podRefInCluster.Status.InitContainerStatuses, containerRemoteStatus)
		} else {
			podRefInCluster.Status.InitContainerStatuses[index] = containerRemoteStatus
		}

		switch {
		case containerRemoteStatus.State.Terminated != nil:
			counterOfTerminatedInitContainers++
			podRefInCluster.Status.InitContainerStatuses[index].State.Terminated.ExitCode = containerRemoteStatus.State.Terminated.ExitCode
			podRefInCluster.Status.InitContainerStatuses[index].State.Terminated.Reason = PodPhaseCompleted
			if containerRemoteStatus.State.Terminated.ExitCode != 0 {
				podErrored = true
				failedReason = "Error: " + strconv.Itoa(int(containerRemoteStatus.State.Terminated.ExitCode))
				podRefInCluster.Status.InitContainerStatuses[index].State.Terminated.Reason = failedReason
				log.G(ctx).Error("Container " + containerRemoteStatus.Name + " exited with error: " + strconv.Itoa(int(containerRemoteStatus.State.Terminated.ExitCode)))
			}
		case containerRemoteStatus.State.Waiting != nil:
			log.G(ctx).Info("Pod " + podRemoteStatus.PodName + ": Service " + containerRemoteStatus.Name + " is setting up on Sidecar")
			podWaitingForInitContainers = true
			podRefInCluster.Status.InitContainerStatuses[index].State.Waiting = containerRemoteStatus.State.Waiting
		case containerRemoteStatus.State.Running != nil:
			podInit = true
			log.G(ctx).Debug("Pod " + podRemoteStatus.PodName + ": Service " + containerRemoteStatus.Name + " is running on Sidecar")
			podRefInCluster.Status.InitContainerStatuses[index].State.Running = containerRemoteStatus.State.Running
			podRefInCluster.Status.InitContainerStatuses[index].State.Waiting = nil
		}
	}
	if counterOfTerminatedInitContainers == nInitContainersInPod {
		podWaitingForInitContainers = false
	}

	return podWaitingForInitContainers, podInit, podErrored, failedReason, counterOfTerminatedInitContainers
}

func handleContainersUpdate(ctx context.Context, podRemoteStatus types.PodStatus, podRefInCluster *v1.Pod, podWaitingForInitContainers bool, podInit bool, nInitContainersInPod int, counterOfTerminatedInitContainers int) (int, bool, string, bool) {
	counterOfTerminatedContainers := 0
	podErrored := false
	failedReason := ""
	podRunning := false

	for _, containerRemoteStatus := range podRemoteStatus.Containers {
		index := 0
		foundCt := false

		for i, checkedContainer := range podRefInCluster.Status.ContainerStatuses {
			if checkedContainer.Name == containerRemoteStatus.Name {
				foundCt = true
				index = i
				break
			}
		}

		// if it is the first time checking the container, append it to the pod containers, otherwise just update the correct item
		if !foundCt {
			podRefInCluster.Status.ContainerStatuses = append(podRefInCluster.Status.ContainerStatuses, containerRemoteStatus)
		} else {
			podRefInCluster.Status.ContainerStatuses[index] = containerRemoteStatus
		}

		// if the pod is waiting for the starting of the init containers or some of them are still running
		// all the other containers are in waiting state
		if podWaitingForInitContainers || podInit {
			podRefInCluster.Status.ContainerStatuses[index].State.Waiting = &v1.ContainerStateWaiting{Reason: "Waiting for init containers"}
			podRefInCluster.Status.ContainerStatuses[index].State.Running = nil
			podRefInCluster.Status.ContainerStatuses[index].State.Terminated = nil
			if podInit {
				podRefInCluster.Status.ContainerStatuses[index].State.Waiting.Reason = "Init:" + strconv.Itoa(counterOfTerminatedInitContainers) + "/" + strconv.Itoa(nInitContainersInPod)
			} else {
				podRefInCluster.Status.ContainerStatuses[index].State.Waiting.Reason = "PodInitializing"
			}
		} else {
			// if plugin cannot return any non-terminated container set the status to terminated
			// if the exit code is != 0 get the error  and set error reason + rememeber to set pod to failed
			switch {
			case containerRemoteStatus.State.Terminated != nil:
				log.G(ctx).Debug("Pod " + podRemoteStatus.PodName + ": Service " + containerRemoteStatus.Name + " is not running on Plugin side")
				counterOfTerminatedContainers++
				podRefInCluster.Status.ContainerStatuses[index].State.Terminated.Reason = PodPhaseCompleted
				if containerRemoteStatus.State.Terminated.ExitCode != 0 {
					podErrored = true
					failedReason = "Error: " + strconv.Itoa(int(containerRemoteStatus.State.Terminated.ExitCode))
					podRefInCluster.Status.ContainerStatuses[index].State.Terminated.Reason = failedReason
					log.G(ctx).Error("Container " + containerRemoteStatus.Name + " exited with error: " + strconv.Itoa(int(containerRemoteStatus.State.Terminated.ExitCode)))
				}
			case containerRemoteStatus.State.Waiting != nil:
				log.G(ctx).Info("Pod " + podRemoteStatus.PodName + ": Service " + containerRemoteStatus.Name + " is setting up on Sidecar")
			case containerRemoteStatus.State.Running != nil:
				podRunning = true
				log.G(ctx).Debug("Pod " + podRemoteStatus.PodName + ": Service " + containerRemoteStatus.Name + " is running on Sidecar")
				podRefInCluster.Status.Phase = v1.PodPhase(v1.PodReady)
				podRefInCluster.Status.ContainerStatuses[index].Ready = true
				podRefInCluster.Status.ContainerStatuses[index].State.Running = containerRemoteStatus.State.Running
			}
		}
	}

	return counterOfTerminatedContainers, podErrored, failedReason, podRunning
}

// checkPodsStatus is regularly called by the VK itself at regular intervals of time to query InterLink for Pods' status.
// It basically append all available pods registered to the VK to a slice and passes this slice to the statusRequest function.
// After the statusRequest returns a response, this function uses that response to update every Pod and Container status.
func checkPodsStatus(ctx context.Context, p *Provider, pod *v1.Pod, token string, config Config) ([]types.PodStatus, error) {
	var ret []types.PodStatus

	// retrieve pod status from remote interlink
	returnVal, err := statusRequest(ctx, config, []*v1.Pod{pod}, token)
	if err != nil {
		return nil, err
	}

	if returnVal != nil {

		err = json.Unmarshal(returnVal, &ret)
		if err != nil {
			errWithContext := fmt.Errorf("error doing Unmarshal() in checkPodsStatus() error detail: %s error: %w", fmt.Sprintf("%#v", err), err)
			return nil, errWithContext
		}

		if len(ret) == 0 {
			log.G(ctx).Warning("No status available from InterLink for pod ", pod.Name, "and Pod uid ", pod.UID)
			return nil, nil
		}

		// if there is a pod status available go ahead to match with the latest state available in etcd
		podRemoteStatus := ret[0]

		log.G(ctx).Debug(fmt.Sprintln("Get status from remote status len: ", len(podRemoteStatus.Containers)))
		// avoid asking for status too early, when etcd as not been updated

		if podRemoteStatus.PodName == "" {
			log.G(ctx).Warning("PodName is empty, skipping")
			return nil, err
		}

		// get pod reference from cluster etcd
		podRefInCluster, err := p.GetPodByUID(ctx, podRemoteStatus.PodNamespace, podRemoteStatus.PodName, k8sTypes.UID(podRemoteStatus.PodUID))
		if err != nil {
			log.G(ctx).Warning(err)
			return nil, err
		}
		log.G(ctx).Debug(fmt.Sprintln("Get pod from k8s cluster status: ", podRefInCluster.Status.ContainerStatuses))

		// if the PodUID match with the one in etcd we are talking of the same thing. GOOD
		if podRemoteStatus.PodUID == string(podRefInCluster.UID) {
			// check if the pod is already in a terminal state (Failed or Succeeded)
			if p.pods[podRemoteStatus.PodUID].Status.Phase == v1.PodFailed || p.pods[podRemoteStatus.PodUID].Status.Phase == v1.PodSucceeded {
				if podRefInCluster.Status.Phase == p.pods[podRemoteStatus.PodUID].Status.Phase {
					log.G(ctx).Debug("Pod " + podRemoteStatus.PodName + " is already in phase " + string(p.pods[podRemoteStatus.PodUID].Status.Phase))
					return nil, err
				}
			}

			podInit := false    // if a init container is running, the other containers phase is PodInitializing
			podRunning := false // if a normale container is running, the phase is PodRunning
			podErrored := false
			podInitErrored := false              // if a container is in error, the phase is PodFailed
			podCompleted := false                // if all containers are terminated, the phase is PodSucceeded, but if one is in error, the phase is PodFailed
			podWaitingForInitContainers := false // if init containers are waiting, the phase is PodPending
			failedReason := ""
			failedReasonInit := ""

			nContainersInPod := 0
			if podRemoteStatus.Containers != nil {
				nContainersInPod = len(podRemoteStatus.Containers)
			}
			counterOfTerminatedContainers := 0

			nInitContainersInPod := 0
			if podRemoteStatus.InitContainers != nil {
				nInitContainersInPod = len(podRemoteStatus.InitContainers)
			}
			counterOfTerminatedInitContainers := 0

			log.G(ctx).Debug("Number of containers in POD:      " + strconv.Itoa(nContainersInPod))
			log.G(ctx).Debug("Number of init containers in POD: " + strconv.Itoa(nInitContainersInPod))

			// if there are init containers, we need to check them first
			if nInitContainersInPod > 0 {
				podWaitingForInitContainers, podInit, podInitErrored, failedReasonInit, counterOfTerminatedInitContainers = handleInitContainersUpdate(ctx, podRemoteStatus, podRefInCluster, nInitContainersInPod)
			}

			if podInitErrored {
				log.G(ctx).Error("At least one init container is in error with reason: " + failedReasonInit)
			}

			// call handleContainersUpdate to update the status of the containers
			counterOfTerminatedContainers, podErrored, failedReason, podRunning = handleContainersUpdate(ctx, podRemoteStatus, podRefInCluster, podWaitingForInitContainers, podInit, nInitContainersInPod, counterOfTerminatedInitContainers)

			if counterOfTerminatedContainers == nContainersInPod {
				podCompleted = true
			}

			if podCompleted {
				// it means that all containers are terminated, check if some of them are errored
				if podErrored || podInitErrored {
					podRefInCluster.Status.Phase = v1.PodFailed
					if podErrored {
						podRefInCluster.Status.Reason = failedReason
					} else {
						podRefInCluster.Status.Reason = failedReasonInit
					}
					// override all the ContainerStatuses to set Reason to failedReason or failedReasonInit
					for i := range podRefInCluster.Status.ContainerStatuses {
						if podErrored {
							podRefInCluster.Status.ContainerStatuses[i].State.Terminated.Reason = failedReason
						} else {
							podRefInCluster.Status.ContainerStatuses[i].State.Terminated.Reason = failedReasonInit
						}
					}
				} else {
					podRefInCluster.Status.Conditions = append(podRefInCluster.Status.Conditions, v1.PodCondition{Type: v1.PodReady, Status: v1.ConditionFalse})
					podRefInCluster.Status.Phase = v1.PodSucceeded
					podRefInCluster.Status.Reason = PodPhaseCompleted
				}
			} else {
				if podInit {
					podRefInCluster.Status.Phase = v1.PodPending
					podRefInCluster.Status.Reason = "Init"
				}
				if podWaitingForInitContainers {
					podRefInCluster.Status.Phase = v1.PodPending
					podRefInCluster.Status.Reason = "Waiting for init containers"
				}
				if podRunning && podRefInCluster.Status.Phase != v1.PodRunning { // do not update the status if it is already running
					podRefInCluster.Status.Phase = v1.PodRunning
					podRefInCluster.Status.Conditions = []v1.PodCondition{{Type: v1.PodReady, Status: v1.ConditionTrue}}
					podRefInCluster.Status.Reason = "Running"
				}
			}
		} else {
			list, err := p.clientSet.CoreV1().Pods(podRemoteStatus.PodNamespace).List(ctx, metav1.ListOptions{})
			if err != nil {
				log.G(ctx).Error(err)
				return nil, err
			}

			pods := list.Items

			for _, pod := range pods {
				if string(pod.UID) == podRemoteStatus.PodUID {
					err = updateCacheRequest(ctx, config, pod, token)
					if err != nil {
						log.G(ctx).Error(err)
						continue
					}
				}
			}

		}

		log.G(ctx).Info("No errors while getting statuses")
		log.G(ctx).Debug(ret)
		return nil, nil
	}

	return nil, err
}
