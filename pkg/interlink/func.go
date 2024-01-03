package interlink

import (
	"path/filepath"

	"github.com/containerd/containerd/log"
	commonIL "github.com/intertwin-eu/interlink/pkg/common"
	v1 "k8s.io/api/core/v1"
)

func getData(pod commonIL.PodCreateRequests) (commonIL.RetrievedPodData, error) {
	var retrieved_data commonIL.RetrievedPodData
	retrieved_data.Pod = pod.Pod
	for _, container := range pod.Pod.Spec.Containers {
		log.G(Ctx).Info("- Retrieving Secrets and ConfigMaps for the Docker Sidecar. Container: " + container.Name)

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

		//var podVolumeSpec *v1.VolumeSource
		for _, cfgMap := range pod.ConfigMaps {
			if cfgMap.Name == mount_var.Name {
				retrieved_data.Name = container.Name
				retrieved_data.ConfigMaps = append(retrieved_data.ConfigMaps, cfgMap)
			}
		}

		for _, scrt := range pod.ConfigMaps {
			if scrt.Name == mount_var.Name {
				retrieved_data.Name = container.Name
				retrieved_data.ConfigMaps = append(retrieved_data.ConfigMaps, scrt)
			}
		}

		for _, vol := range pod.Pod.Spec.Volumes {

			if vol.EmptyDir != nil {
				edPath := filepath.Join(commonIL.InterLinkConfigInst.DataRootFolder, pod.Pod.Namespace+"-"+string(pod.Pod.UID)+"/"+"emptyDirs/"+vol.Name)

				retrieved_data.Name = container.Name
				retrieved_data.EmptyDirs = append(retrieved_data.EmptyDirs, edPath)
			}
		}

	}
	return retrieved_data, nil
}
