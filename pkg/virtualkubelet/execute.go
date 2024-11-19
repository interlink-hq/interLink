package virtualkubelet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/containerd/containerd/log"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	trace "go.opentelemetry.io/otel/trace"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	types "github.com/intertwin-eu/interlink/pkg/interlink"
)

const PodPhaseInitialize = "Initializing"

func failedMount(ctx context.Context, failed *bool, name string, pod *v1.Pod, p *Provider) error {
	*failed = true
	log.G(ctx).Warning("Unable to find ConfigMap " + name + " for pod " + pod.Name + ". Waiting for it to be initialized")
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

func doRequest(req *http.Request, token string) (*http.Response, error) {

	if token != "" {
		req.Header.Add("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	return http.DefaultClient.Do(req)

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
func PingInterLink(ctx context.Context, config Config) (bool, int, error) {
	tracer := otel.Tracer("interlink-service")
	interLinkEndpoint := getSidecarEndpoint(ctx, config.InterlinkURL, config.Interlinkport)
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
			return false, retVal, err
		}
		req.Header.Add("Authorization", "Bearer "+string(token))
	}

	startHTTPCall := time.Now().UnixMicro()
	_, spanHTTP := tracer.Start(ctx, "PingHttpCall", trace.WithAttributes(
		attribute.Int64("start.timestamp", startHTTPCall),
	))
	defer spanHTTP.End()
	defer types.SetDurationSpan(startHTTPCall, spanHTTP)

	resp, err := http.DefaultClient.Do(req)

	if err != nil {
		spanHTTP.SetAttributes(attribute.Int("exit.code", http.StatusInternalServerError))
		return false, retVal, err
	}
	defer resp.Body.Close()

	types.SetDurationSpan(startHTTPCall, spanHTTP, types.WithHTTPReturnCode(resp.StatusCode))
	retBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		log.G(ctx).Error(err)
		return false, retVal, err
	}
	retVal, err = strconv.Atoi(string(retBytes))
	if err != nil {
		log.G(ctx).Error(err)
		return false, retVal, err
	}

	if resp.StatusCode != http.StatusOK {
		log.G(ctx).Error("server error: " + fmt.Sprint(resp.StatusCode))
		return false, retVal, nil
	}

	return true, retVal, nil
}

// updateCacheRequest is called when the VK receives the status of a pod already deleted. It performs a REST call InterLink API to update the cache deleting that pod from the cached structure
func updateCacheRequest(ctx context.Context, config Config, pod v1.Pod, token string) error {
	bodyBytes, err := json.Marshal(pod)
	if err != nil {
		log.L.Error(err)
		return err
	}

	interLinkEndpoint := getSidecarEndpoint(ctx, config.InterlinkURL, config.Interlinkport)
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

	resp, err := http.DefaultClient.Do(req)
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
	interLinkEndpoint := getSidecarEndpoint(ctx, config.InterlinkURL, config.Interlinkport)

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

	resp, err := doRequest(req, token)
	if err != nil {
		log.L.Error(err)
		return nil, err
	}
	defer resp.Body.Close()

	types.SetDurationSpan(startHTTPCall, spanHTTP, types.WithHTTPReturnCode(resp.StatusCode))

	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("Unexpected error occured while creating Pods. Status code: " + strconv.Itoa(resp.StatusCode) + ". Check InterLink's logs for further informations")
	}
	returnValue, err := io.ReadAll(resp.Body)
	if err != nil {
		log.L.Error(err)
		return nil, err
	}

	return returnValue, nil
}

// deleteRequest performs a REST call to the InterLink API when a Pod is deleted from the VK. It Marshals the standard v1.Pod struct and sends it to InterLink.
// Returns the call response expressed in bytes and/or the first encountered error
func deleteRequest(ctx context.Context, config Config, pod *v1.Pod, token string) ([]byte, error) {
	interLinkEndpoint := getSidecarEndpoint(ctx, config.InterlinkURL, config.Interlinkport)
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

	resp, err := doRequest(req, token)
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
	var response []types.PodStatus
	err = json.Unmarshal(returnValue, &response)
	if err != nil {
		log.G(context.Background()).Error(err)
		return nil, err
	}

	return returnValue, nil
}

// statusRequest performs a REST call to the InterLink API when the VK needs an update on its Pods' status. A Marshalled slice of v1.Pod is sent to the InterLink API,
// to query the below plugin for their status.
// Returns the call response expressed in bytes and/or the first encountered error
func statusRequest(ctx context.Context, config Config, podsList []*v1.Pod, token string) ([]byte, error) {
	tracer := otel.Tracer("interlink-service")

	interLinkEndpoint := getSidecarEndpoint(ctx, config.InterlinkURL, config.Interlinkport)

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

	resp, err := doRequest(req, token)
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
func LogRetrieval(ctx context.Context, config Config, logsRequest types.LogStruct) (io.ReadCloser, error) {
	tracer := otel.Tracer("interlink-service")
	interLinkEndpoint := getSidecarEndpoint(ctx, config.InterlinkURL, config.Interlinkport)

	token := ""

	if config.VKTokenFile != "" {
		b, err := os.ReadFile(config.VKTokenFile) // just pass the file name
		if err != nil {
			log.G(ctx).Fatal(err)
		}
		token = string(b)
	}

	bodyBytes, err := json.Marshal(logsRequest)
	if err != nil {
		log.G(ctx).Error(err)
		return nil, err
	}
	reader := bytes.NewReader(bodyBytes)
	req, err := http.NewRequest(http.MethodGet, interLinkEndpoint+"/getLogs", reader)
	if err != nil {
		log.G(ctx).Error(err)
		return nil, err
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

	resp, err := doRequest(req, token)
	if err != nil {
		log.G(ctx).Error(err)
		return nil, err
	}
	// defer resp.Body.Close()

	types.SetDurationSpan(startHTTPCall, spanHTTP, types.WithHTTPReturnCode(resp.StatusCode))
	if resp.StatusCode != http.StatusOK {
		err = errors.New("Unexpected error occured while getting logs. Status code: " + strconv.Itoa(resp.StatusCode) + ". Check InterLink's logs for further informations")
	}

	// return io.NopCloser(bufio.NewReader(resp.Body)), err
	return resp.Body, err
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
	switch mode {
	case CREATE:
		var req types.PodCreateRequests
		var resp types.CreateStruct

		req.Pod = *pod
		startTime := time.Now()

		timeNow := time.Now()
		_, err := p.clientSet.CoreV1().Pods(pod.Namespace).Get(ctx, pod.Name, metav1.GetOptions{})
		if err != nil {
			log.G(ctx).Warning("Deleted Pod before actual creation")
			return nil
		}

		var failed bool

		for _, volume := range pod.Spec.Volumes {
			for {
				if timeNow.Sub(startTime).Seconds() < time.Hour.Minutes()*5 {
					if volume.ConfigMap != nil {
						cfgmap, err := p.clientSet.CoreV1().ConfigMaps(pod.Namespace).Get(ctx, volume.ConfigMap.Name, metav1.GetOptions{})
						if err != nil {
							err = failedMount(ctx, &failed, volume.ConfigMap.Name, pod, p)
							if err != nil {
								return err
							}
						} else {
							failed = false
							req.ConfigMaps = append(req.ConfigMaps, *cfgmap)
						}
					} else if volume.Secret != nil {
						scrt, err := p.clientSet.CoreV1().Secrets(pod.Namespace).Get(ctx, volume.Secret.SecretName, metav1.GetOptions{})
						if err != nil {
							err = failedMount(ctx, &failed, volume.Secret.SecretName, pod, p)
							if err != nil {
								return err
							}
						} else {
							failed = false
							req.Secrets = append(req.Secrets, *scrt)
						}
					}

					if failed {
						time.Sleep(time.Second)
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
				return errors.New("unable to retrieve ConfigMaps or Secrets. Check logs")
			}
		}

		returnVal, err := createRequest(ctx, config, req, token)
		if err != nil {
			return err
		}

		// get remote job ID and annotate it into the pod
		err = json.Unmarshal(returnVal, &resp)
		if err != nil {
			return err
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

// checkPodsStatus is regularly called by the VK itself at regular intervals of time to query InterLink for Pods' status.
// It basically append all available pods registered to the VK to a slice and passes this slice to the statusRequest function.
// After the statusRequest returns a response, this function uses that response to update every Pod and Container status.
func checkPodsStatus(ctx context.Context, p *Provider, podsList []*v1.Pod, token string, config Config) ([]types.PodStatus, error) {
	var ret []types.PodStatus
	// commented out because it's too verbose. uncomment to see all registered pods
	// log.G(ctx).Debug(p.pods)

	// retrieve pod status from remote interlink
	returnVal, err := statusRequest(ctx, config, podsList, token)
	if err != nil {
		return nil, err
	}

	if returnVal != nil {

		err = json.Unmarshal(returnVal, &ret)
		if err != nil {
			return nil, err
		}

		// if there is a pod status available go ahead to match with the latest state available in etcd
		if podsList != nil {
			for _, podRemoteStatus := range ret {

				log.G(ctx).Debug(fmt.Sprintln("Get status from remote status len: ", len(podRemoteStatus.Containers)))
				// avoid asking for status too early, when etcd as not been updated
				if podRemoteStatus.PodName != "" {

					// get pod reference from cluster etcd
					podRefInCluster, err := p.GetPod(ctx, podRemoteStatus.PodNamespace, podRemoteStatus.PodName)
					if err != nil {
						log.G(ctx).Warning(err)
						continue
					}
					log.G(ctx).Debug(fmt.Sprintln("Get pod from k8s cluster status: ", podRefInCluster.Status.ContainerStatuses))

					// if the PodUID match with the one in etcd we are talking of the same thing. GOOD
					if podRemoteStatus.PodUID == string(podRefInCluster.UID) {
						podRunning := false
						podErrored := false
						podCompleted := false
						failedReason := ""

						// For each container of the pod we check if there is a previous state known by K8s
						for _, containerRemoteStatus := range podRemoteStatus.Containers {
							index := 0
							foundCt := false

							for i, checkedContainer := range podRefInCluster.Status.ContainerStatuses {
								if checkedContainer.Name == containerRemoteStatus.Name {
									foundCt = true
									index = i
								}
							}

							// if it is the first time checking the container, append it to the pod containers, otherwise just update the correct item
							if !foundCt {
								podRefInCluster.Status.ContainerStatuses = append(podRefInCluster.Status.ContainerStatuses, containerRemoteStatus)
							} else {
								podRefInCluster.Status.ContainerStatuses[index] = containerRemoteStatus
							}

							log.G(ctx).Debug(containerRemoteStatus.State.Running)

							// if plugin cannot return any non-terminated container set the status to terminated
							// if the exit code is != 0 get the error  and set error reason + rememeber to set pod to failed
							switch {
							case containerRemoteStatus.State.Terminated != nil:
								log.G(ctx).Debug("Pod " + podRemoteStatus.PodName + ": Service " + containerRemoteStatus.Name + " is not running on Plugin side")
								podCompleted = true
								podRefInCluster.Status.ContainerStatuses[index].State.Terminated.Reason = "Completed"
								if containerRemoteStatus.State.Terminated.ExitCode != 0 {
									podErrored = true
									failedReason = "Error: " + strconv.Itoa(int(containerRemoteStatus.State.Terminated.ExitCode))
									podRefInCluster.Status.ContainerStatuses[index].State.Terminated.Reason = failedReason
									log.G(ctx).Error("Container " + containerRemoteStatus.Name + " exited with error: " + strconv.Itoa(int(containerRemoteStatus.State.Terminated.ExitCode)))
								}
							case containerRemoteStatus.State.Waiting != nil:
								log.G(ctx).Info("Pod " + podRemoteStatus.PodName + ": Service " + containerRemoteStatus.Name + " is setting up on Sidecar")
								podRunning = true
							case containerRemoteStatus.State.Running != nil:
								podRunning = true
								log.G(ctx).Debug("Pod " + podRemoteStatus.PodName + ": Service " + containerRemoteStatus.Name + " is running on Sidecar")
							}

							// if this is the first time you see a container running/errored/completed, update the status of the pod.
							switch {
							case podRunning && podRefInCluster.Status.Phase != v1.PodRunning:
								podRefInCluster.Status.Phase = v1.PodRunning
								podRefInCluster.Status.Conditions = append(podRefInCluster.Status.Conditions, v1.PodCondition{Type: v1.PodReady, Status: v1.ConditionTrue})
							case podErrored && podRefInCluster.Status.Phase != v1.PodFailed:
								podRefInCluster.Status.Phase = v1.PodFailed
								podRefInCluster.Status.Reason = failedReason
							case podCompleted && podRefInCluster.Status.Phase != v1.PodSucceeded:
								podRefInCluster.Status.Conditions = append(podRefInCluster.Status.Conditions, v1.PodCondition{Type: v1.PodReady, Status: v1.ConditionFalse})
								podRefInCluster.Status.Phase = v1.PodSucceeded
								podRefInCluster.Status.Reason = "Completed"
							}

						}
					} else {

						// if you don't now any UID yet, collect the status and updated the status cache
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
				}
			}
			log.G(ctx).Info("No errors while getting statuses")
			log.G(ctx).Debug(ret)
			return nil, nil
		}

	}

	return nil, err
}
