package interlink

import (
	"path/filepath"

	"github.com/containerd/containerd/log"
	commonIL "github.com/intertwin-eu/interlink/pkg/common"
	v1 "k8s.io/api/core/v1"
)

var PodStatuses []commonIL.PodStatus

func getData(pod commonIL.PodCreateRequests) (commonIL.RetrievedPodData, error) {
	log.G(Ctx).Debug(pod.ConfigMaps)
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

func updateStatuses(statuses []commonIL.PodStatus) {
	for _, podStatus := range statuses {
		updated := false
		for i, podStatus2 := range PodStatuses {
			if podStatus.PodUID == podStatus2.PodUID {
				PodStatuses[i] = podStatus
				updated = true
				break
			}
		}
		if !updated {
			PodStatuses = append(PodStatuses, podStatus)
		}
	}
}

func deleteCachedStatus(uid string) {
	for i, status := range PodStatuses {
		if status.PodUID == uid {
			PodStatuses = append(PodStatuses[:i], PodStatuses[i+1:]...)
			return
		}
	}
}

func checkIfCached(uid string) bool {
	for _, podStatus := range PodStatuses {
		if podStatus.PodUID == uid {
			return true
		}
	}
	return false
}
