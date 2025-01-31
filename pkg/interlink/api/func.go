package api

import (
	"context"
	"strings"
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
func getData(ctx context.Context, config types.Config, pod types.PodCreateRequests, span trace.Span) (types.RetrievedPodData, error) {
	start := time.Now().UnixMicro()
	span.AddEvent("Retrieving data for pod " + pod.Pod.Name)
	log.G(ctx).Debug(pod.ConfigMaps)
	var retrievedData types.RetrievedPodData
	retrievedData.Pod = pod.Pod

	for _, container := range pod.Pod.Spec.InitContainers {
		startContainer := time.Now().UnixMicro()
		log.G(ctx).Info("- Retrieving Secrets and ConfigMaps for the Docker Sidecar. InitContainer: " + container.Name)
		log.G(ctx).Debug(container.VolumeMounts)
		data, err := retrieveData(ctx, config, pod, container)
		if err != nil {
			log.G(ctx).Error(err)
			return types.RetrievedPodData{}, err
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
func retrieveData(ctx context.Context, config types.Config, pod types.PodCreateRequests, container v1.Container) (types.RetrievedContainer, error) {
	retrievedData := types.RetrievedContainer{}
	retrievedData.Name = container.Name
	for _, mountVar := range container.VolumeMounts {
		log.G(ctx).Debug("-- Retrieving data for mountpoint ", mountVar.Name)

	loopVolumes:
		for _, vol := range pod.Pod.Spec.Volumes {
			if vol.Name == mountVar.Name {
				switch {
				case vol.ConfigMap != nil:
					log.G(ctx).Info("--- Retrieving ConfigMap ", vol.ConfigMap.Name)
					for _, cfgMap := range pod.ConfigMaps {
						if cfgMap.Name == vol.ConfigMap.Name {
							log.G(ctx).Debug("configMap found! Name: ", cfgMap.Name)
							retrievedData.ConfigMaps = append(retrievedData.ConfigMaps, cfgMap)
							break loopVolumes
						}
					}
					// This should not happen, error. Building error context.
					var configMapsKeys []string
					for _, cfgMap := range pod.ConfigMaps {
						configMapsKeys = append(configMapsKeys, cfgMap.Name)
					}
					log.G(ctx).Errorf("could not find in retrievedData the matching object for volume: %s (pod: %s container: %s configMap: %s) retrievedData keys: %s", vol.Name,
						pod.Pod.Name, container.Name, vol.ConfigMap.Name, strings.Join(configMapsKeys, ","))

				case vol.Projected != nil:
					log.G(ctx).Info("--- Retrieving ProjectedVolume ", vol.Name)
					for _, projectedVolumeMap := range pod.ProjectedVolumeMaps {
						log.G(ctx).Debug("Comparing projectedVolumeMap.Name: ", projectedVolumeMap.Name, " with vol.Name: ", vol.Name)
						if projectedVolumeMap.Name == vol.Name {
							log.G(ctx).Debug("projectedVolumeMap found! Name: ", projectedVolumeMap.Name)

							retrievedData.ProjectedVolumeMaps = append(retrievedData.ProjectedVolumeMaps, projectedVolumeMap)
							break loopVolumes
						}
					}
					// This should not happen, error. Building error context.
					var projectedVolumeMapsKeys []string
					for _, projectedVolumeMap := range pod.ProjectedVolumeMaps {
						projectedVolumeMapsKeys = append(projectedVolumeMapsKeys, projectedVolumeMap.Name)
					}
					log.G(ctx).Errorf("could not find in retrievedData the matching object for  volume: %s (pod: %s container: %s projectedVolumeMap) retrievedData keys: %s",
						vol.Name, pod.Pod.Name, container.Name, strings.Join(projectedVolumeMapsKeys, ","))

				case vol.Secret != nil:
					log.G(ctx).Info("--- Retrieving Secret ", vol.Secret.SecretName)
					for _, secret := range pod.Secrets {
						if secret.Name == vol.Secret.SecretName {
							log.G(ctx).Debug("secret found! Name: ", secret.Name)
							retrievedData.Secrets = append(retrievedData.Secrets, secret)
							break loopVolumes
						}
					}
					// This should not happen, error. Building error context.
					var secretKeys []string
					for _, secret := range pod.Secrets {
						secretKeys = append(secretKeys, secret.Name)
					}
					log.G(ctx).Errorf("could not find in retrievedData the matching object for volume: %s (pod: %s container: %s secret: %s) retrievedData keys: %s",
						pod.Pod.Name, container.Name, vol.Name, vol.Secret.SecretName, strings.Join(secretKeys, ","))

				case vol.EmptyDir != nil:
					// Deprecated: EmptyDirs is useless at VK level. It should be moved to plugin level.
					// edPath := filepath.Join(config.DataRootFolder, pod.Pod.Namespace+"-"+string(pod.Pod.UID), "emptyDirs", vol.Name)
					// retrievedData.EmptyDirs = append(retrievedData.EmptyDirs, edPath)

				default:
					log.G(ctx).Warning("ignoring unsupported volume type for ", mountVar.Name)
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

	return ok
}

// updateStatuses locks and updates the PodStatuses map with the statuses contained in the returnedStatuses slice
func updateStatuses(returnedStatuses []types.PodStatus) {
	PodStatuses.mu.Lock()

	for _, new := range returnedStatuses {
		// log.G(ctx).Debug(PodStatuses.Statuses, new)
		PodStatuses.Statuses[new.PodUID] = new
	}

	PodStatuses.mu.Unlock()
}
