package interlink

import (
	"path/filepath"

	"github.com/containerd/containerd/log"
	commonIL "github.com/intertwin-eu/interlink/pkg/common"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func getData(pod *v1.Pod) ([]commonIL.RetrievedPodData, error) {
	var retrieved_data []commonIL.RetrievedPodData
	for _, container := range pod.Spec.Containers {
		log.G(Ctx).Info("- Retrieving Secrets and ConfigMaps for the Docker Sidecar. Container: " + container.Name)

		data, err := retrieve_data(container, pod)
		if err != nil {
			log.G(Ctx).Error(err)
			return nil, err
		}

		if data.Containers != nil {
			data.Pod = *pod
			retrieved_data = append(retrieved_data, data)
		}
	}

	return retrieved_data, nil
}

func retrieve_data(container v1.Container, pod *v1.Pod) (commonIL.RetrievedPodData, error) {
	retrieved_data := commonIL.RetrievedPodData{}
	for _, mount_var := range container.VolumeMounts {
		log.G(Ctx).Debug("-- Retrieving data for mountpoint " + mount_var.Name)

		var podVolumeSpec *v1.VolumeSource

		for _, vol := range pod.Spec.Volumes {

			if vol.Name == mount_var.Name {
				podVolumeSpec = &vol.VolumeSource
			}

			if podVolumeSpec != nil && podVolumeSpec.ConfigMap != nil {
				log.G(Ctx).Info("--- Retrieving ConfigMap " + podVolumeSpec.ConfigMap.Name)
				cmvs := podVolumeSpec.ConfigMap

				configMap, err := Clientset.CoreV1().ConfigMaps(pod.Namespace).Get(Ctx, cmvs.Name, metav1.GetOptions{})

				if err != nil {
					log.G(Ctx).Error(err)
					return commonIL.RetrievedPodData{}, err
				} else {
					log.G(Ctx).Debug("---- Retrieved ConfigMap " + podVolumeSpec.ConfigMap.Name)
				}

				if configMap != nil {
					if retrieved_data.Containers == nil {
						retrieved_data.Containers = append(retrieved_data.Containers, commonIL.RetrievedContainer{Name: container.Name})
					}
					retrieved_data.Containers[len(retrieved_data.Containers)-1].ConfigMaps = append(retrieved_data.Containers[len(retrieved_data.Containers)-1].ConfigMaps, *configMap)
				}

			} else if podVolumeSpec != nil && podVolumeSpec.Secret != nil {
				log.G(Ctx).Info("--- Retrieving Secret " + podVolumeSpec.Secret.SecretName)
				svs := podVolumeSpec.Secret

				secret, err := Clientset.CoreV1().Secrets(pod.Namespace).Get(Ctx, svs.SecretName, metav1.GetOptions{})

				if err != nil {
					log.G(Ctx).Error(err)
					return commonIL.RetrievedPodData{}, err
				} else {
					log.G(Ctx).Debug("---- Retrieved Secret " + svs.SecretName)
				}

				if secret.Data != nil {
					if retrieved_data.Containers == nil {
						retrieved_data.Containers = append(retrieved_data.Containers, commonIL.RetrievedContainer{Name: container.Name})
					}
					retrieved_data.Containers[len(retrieved_data.Containers)-1].Secrets = append(retrieved_data.Containers[len(retrieved_data.Containers)-1].Secrets, *secret)
				}

			} else if podVolumeSpec != nil && podVolumeSpec.EmptyDir != nil {
				edPath := filepath.Join(commonIL.InterLinkConfigInst.DataRootFolder, pod.Namespace+"-"+string(pod.UID)+"/"+"emptyDirs/"+vol.Name)
				if retrieved_data.Containers == nil {
					retrieved_data.Containers = append(retrieved_data.Containers, commonIL.RetrievedContainer{Name: container.Name})
				}
				retrieved_data.Containers[len(retrieved_data.Containers)-1].EmptyDirs = append(retrieved_data.Containers[len(retrieved_data.Containers)-1].EmptyDirs, edPath)
			}
		}
	}
	return retrieved_data, nil
}
