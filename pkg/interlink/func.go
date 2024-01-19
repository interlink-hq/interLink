package interlink

import (
	"path/filepath"
	"sync"

	"github.com/containerd/containerd/log"
	commonIL "github.com/intertwin-eu/interlink/pkg/common"
	v1 "k8s.io/api/core/v1"
)

type MutexStatuses struct {
	mu       sync.Mutex
	Statuses map[string]commonIL.PodStatus
}

var PodStatuses MutexStatuses

func getData(pod commonIL.PodCreateRequests) (commonIL.RetrievedPodData, error) {
	var retrieved_data commonIL.RetrievedPodData
	retrieved_data.Pod = pod.Pod
	for _, container := range pod.Pod.Spec.Containers {
		log.G(Ctx).Info("- Retrieving Secrets and ConfigMaps for the Docker Sidecar. Container: " + container.Name)
		log.G(Ctx).Debug(container.VolumeMounts)
		data, err := retrieve_data(container, pod)
		if err != nil {
			log.G(Ctx).Error(err)
			return commonIL.RetrievedPodData{}, err
		}
		retrieved_data.Containers = append(retrieved_data.Containers, data)
	}

	return retrieved_data, nil
}

func retrieve_data(container v1.Container, pod commonIL.PodCreateRequests) (commonIL.RetrievedContainer, error) {
	retrieved_data := commonIL.RetrievedContainer{}
	for _, mount_var := range container.VolumeMounts {
		log.G(Ctx).Debug("-- Retrieving data for mountpoint " + mount_var.Name)

		for _, vol := range pod.Pod.Spec.Volumes {
			if vol.Name == mount_var.Name {
				if vol.ConfigMap != nil {

					log.G(Ctx).Info("--- Retrieving ConfigMap " + vol.ConfigMap.Name)
					retrieved_data.Name = container.Name
					for _, cfgMap := range pod.ConfigMaps {
						if cfgMap.Name == vol.ConfigMap.Name {
							retrieved_data.Name = container.Name
							retrieved_data.ConfigMaps = append(retrieved_data.ConfigMaps, cfgMap)
						}
					}

				} else if vol.Secret != nil {

					log.G(Ctx).Info("--- Retrieving Secret " + vol.Secret.SecretName)
					retrieved_data.Name = container.Name
					for _, secret := range pod.Secrets {
						if secret.Name == vol.Secret.SecretName {
							retrieved_data.Name = container.Name
							retrieved_data.Secrets = append(retrieved_data.Secrets, secret)
						}
					}

				} else if vol.EmptyDir != nil {
					edPath := filepath.Join(commonIL.InterLinkConfigInst.DataRootFolder, pod.Pod.Namespace+"-"+string(pod.Pod.UID)+"/"+"emptyDirs/"+vol.Name)

					retrieved_data.Name = container.Name
					retrieved_data.EmptyDirs = append(retrieved_data.EmptyDirs, edPath)
				}
			}
		}
	}
	return retrieved_data, nil
}

func deleteCachedStatus(uid string) {
	PodStatuses.mu.Lock()
	delete(PodStatuses.Statuses, uid)
	PodStatuses.mu.Unlock()
}

func checkIfCached(uid string) bool {
	podStatus, ok := PodStatuses.Statuses[uid]

	if &podStatus != nil && ok {
		return true
	} else {
		return false
	}
}

func updateStatuses(returnedStatuses []commonIL.PodStatus) {
	PodStatuses.mu.Lock()

	for _, new := range returnedStatuses {
		log.G(Ctx).Info(PodStatuses.Statuses, new)
		if checkIfCached(new.PodUID) {
			PodStatuses.Statuses[new.PodUID] = new
		} else {
			PodStatuses.Statuses[new.PodUID] = new
		}
	}

	PodStatuses.mu.Unlock()
}
