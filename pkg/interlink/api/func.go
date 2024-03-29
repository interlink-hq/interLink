package api

import (
	"context"
	"path/filepath"
	"sync"

	"github.com/containerd/containerd/log"
	v1 "k8s.io/api/core/v1"

	commonIL "github.com/intertwin-eu/interlink/pkg/interlink"
)

type MutexStatuses struct {
	mu       sync.Mutex
	Statuses map[string]commonIL.PodStatus
}

var PodStatuses MutexStatuses

// getData retrieves ConfigMaps, Secrets and EmptyDirs from the provided pod by calling the retrieveData function.
// The config is needed by the retrieveData function.
// The function aggregates the return values of retrieveData function in a commonIL.RetrievedPodData variable and returns it, along with the first encountered error.
func getData(ctx context.Context, config commonIL.InterLinkConfig, pod commonIL.PodCreateRequests) (commonIL.RetrievedPodData, error) {
	log.G(ctx).Debug(pod.ConfigMaps)
	var retrievedData commonIL.RetrievedPodData
	retrievedData.Pod = pod.Pod
	for _, container := range pod.Pod.Spec.Containers {
		log.G(ctx).Info("- Retrieving Secrets and ConfigMaps for the Docker Sidecar. Container: " + container.Name)
		log.G(ctx).Debug(container.VolumeMounts)
		data, err := retrieveData(ctx, config, pod, container)
		if err != nil {
			log.G(ctx).Error(err)
			return commonIL.RetrievedPodData{}, err
		}
		retrievedData.Containers = append(retrievedData.Containers, data)
	}

	return retrievedData, nil
}

// retrieveData retrieves ConfigMaps, Secrets and EmptyDirs.
// The config is needed to specify the EmptyDirs mounting point.
// It returns the retrieved data in a variable of type commonIL.RetrievedContainer and the first encountered error.
func retrieveData(ctx context.Context, config commonIL.InterLinkConfig, pod commonIL.PodCreateRequests, container v1.Container) (commonIL.RetrievedContainer, error) {
	retrievedData := commonIL.RetrievedContainer{}
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
func updateStatuses(returnedStatuses []commonIL.PodStatus) {
	PodStatuses.mu.Lock()

	for _, new := range returnedStatuses {
		//log.G(ctx).Debug(PodStatuses.Statuses, new)
		PodStatuses.Statuses[new.PodUID] = new
	}

	PodStatuses.mu.Unlock()
}
