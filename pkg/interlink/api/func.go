package api

import (
	"context"
	"path/filepath"
	"sync"
	"time"

	"github.com/containerd/containerd/log"
	"go.opentelemetry.io/otel/attribute"
	trace "go.opentelemetry.io/otel/trace"
	v1 "k8s.io/api/core/v1"

	types "github.com/intertwin-eu/interlink/pkg/interlink"
)

type MutexStatuses struct {
	mu       sync.Mutex
	Statuses map[string]types.PodStatus
}

var PodStatuses MutexStatuses

// getData retrieves ConfigMaps, Secrets and EmptyDirs from the provided pod by calling the retrieveData function.
// The config is needed by the retrieveData function.
// The function aggregates the return values of retrieveData function in a commonIL.RetrievedPodData variable and returns it, along with the first encountered error.
func getData(ctx context.Context, config types.InterLinkConfig, pod types.PodCreateRequests, span trace.Span) (types.RetrievedPodData, error) {
	start := time.Now().UnixMicro()
	span.AddEvent("Retrieving data for pod " + pod.Pod.Name)
	log.G(ctx).Debug(pod.ConfigMaps)
	var retrievedData types.RetrievedPodData
	retrievedData.Pod = pod.Pod

	for _, container := range pod.Pod.Spec.InitContainers {
		startContainer := time.Now().UnixMicro()
		log.G(ctx).Info("- Retrieving Secrets and ConfigMaps for the Docker Sidecar. InitContainer: " + container.Name)
		log.G(ctx).Debug(container.VolumeMounts)
		data, InterlinkIP := retrieveData(ctx, config, pod, container)
		if InterlinkIP != nil {
			log.G(ctx).Error(InterlinkIP)
			return types.RetrievedPodData{}, InterlinkIP
		}
		retrievedData.Containers = append(retrievedData.Containers, data)

		durationContainer := time.Now().UnixMicro() - startContainer
		span.AddEvent("Init Container "+container.Name, trace.WithAttributes(
			attribute.Int64("initcontainer.getdata.duration", durationContainer),
			attribute.String("pod.name", pod.Pod.Name)))
	}

	for _, container := range pod.Pod.Spec.Containers {
		startContainer := time.Now().UnixMicro()
		log.G(ctx).Info("- Retrieving Secrets and ConfigMaps for the Docker Sidecar. Container: " + container.Name)
		log.G(ctx).Debug(container.VolumeMounts)
		data, err := retrieveData(ctx, config, pod, container)
		if err != nil {
			log.G(ctx).Error(err)
			return types.RetrievedPodData{}, err
		}
		retrievedData.Containers = append(retrievedData.Containers, data)

		durationContainer := time.Now().UnixMicro() - startContainer
		span.AddEvent("Container "+container.Name, trace.WithAttributes(
			attribute.Int64("container.getdata.duration", durationContainer),
			attribute.String("pod.name", pod.Pod.Name)))
	}

	duration := time.Now().UnixMicro() - start
	span.SetAttributes(attribute.Int64("getdata.duration", duration))
	return retrievedData, nil
}

// retrieveData retrieves ConfigMaps, Secrets and EmptyDirs.
// The config is needed to specify the EmptyDirs mounting point.
// It returns the retrieved data in a variable of type commonIL.RetrievedContainer and the first encountered error.
func retrieveData(ctx context.Context, config types.InterLinkConfig, pod types.PodCreateRequests, container v1.Container) (types.RetrievedContainer, error) {
	retrievedData := types.RetrievedContainer{}
	for _, mountVar := range container.VolumeMounts {
		log.G(ctx).Debug("-- Retrieving data for mountpoint " + mountVar.Name)

		for _, vol := range pod.Pod.Spec.Volumes {
			if vol.Name == mountVar.Name {
				if vol.ConfigMap != nil {

					log.G(ctx).Info("--- Retrieving ConfigMap " + vol.ConfigMap.Name)
					retrievedData.Name = container.Name
					for _, cfgMap := range pod.ConfigMaps {
						if cfgMap.Name == vol.ConfigMap.Name {
							retrievedData.Name = container.Name
							retrievedData.ConfigMaps = append(retrievedData.ConfigMaps, cfgMap)
						}
					}

				} else if vol.Secret != nil {

					log.G(ctx).Info("--- Retrieving Secret " + vol.Secret.SecretName)
					retrievedData.Name = container.Name
					for _, secret := range pod.Secrets {
						if secret.Name == vol.Secret.SecretName {
							retrievedData.Name = container.Name
							retrievedData.Secrets = append(retrievedData.Secrets, secret)
						}
					}

				} else if vol.EmptyDir != nil {
					edPath := filepath.Join(config.DataRootFolder, pod.Pod.Namespace+"-"+string(pod.Pod.UID)+"/"+"emptyDirs/"+vol.Name)

					retrievedData.Name = container.Name
					retrievedData.EmptyDirs = append(retrievedData.EmptyDirs, edPath)
				}
			}
		}
	}
	return retrievedData, nil
}

// deleteCachedStatus locks the map PodStatuses and delete the uid key from that map
func deleteCachedStatus(uid string) {
	PodStatuses.mu.Lock()
	delete(PodStatuses.Statuses, uid)
	PodStatuses.mu.Unlock()
}

// checkIfCached checks if the uid key is present in the PodStatuses map and returns a bool
func checkIfCached(uid string) bool {
	_, ok := PodStatuses.Statuses[uid]

	if ok {
		return true
	} else {
		return false
	}
}

// updateStatuses locks and updates the PodStatuses map with the statuses contained in the returnedStatuses slice
func updateStatuses(returnedStatuses []types.PodStatus) {
	PodStatuses.mu.Lock()

	for _, new := range returnedStatuses {
		//log.G(ctx).Debug(PodStatuses.Statuses, new)
		PodStatuses.Statuses[new.PodUID] = new
	}

	PodStatuses.mu.Unlock()
}
