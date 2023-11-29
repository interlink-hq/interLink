package interlink

import (
	"path/filepath"

	"github.com/containerd/containerd/log"
	commonIL "github.com/intertwin-eu/interlink/pkg/common"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func getData(pod *v1.Pod) (commonIL.RetrievedPodData, error) {
	var retrieved_data commonIL.RetrievedPodData
	retrieved_data.Pod = *pod
	for _, container := range pod.Spec.Containers {
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

func retrieve_data(container v1.Container, pod *v1.Pod) (commonIL.RetrievedContainer, error) {
	retrieved_data := commonIL.RetrievedContainer{}
	for _, mount_var := range container.VolumeMounts {
		log.G(Ctx).Debug("-- Retrieving data for mountpoint " + mount_var.Name)

		//var podVolumeSpec *v1.VolumeSource

		for _, vol := range pod.Spec.Volumes {

			if vol.Name == mount_var.Name {
				if vol.ConfigMap != nil {
					log.G(Ctx).Info("--- Retrieving ConfigMap " + vol.ConfigMap.Name)
					cmvs := vol.ConfigMap

					configMap, err := Clientset.CoreV1().ConfigMaps(pod.Namespace).Get(Ctx, cmvs.Name, metav1.GetOptions{})

          if err != nil {
						log.G(Ctx).Error(err)
						return commonIL.RetrievedContainer{}, err
					} else {
						log.G(Ctx).Debug("---- Retrieved ConfigMap " + vol.ConfigMap.Name)
					}

					if configMap != nil {
						retrieved_data.Name = container.Name
						retrieved_data.ConfigMaps = append(retrieved_data.ConfigMaps, *configMap)
					}

				} else if vol.Secret != nil {
					log.G(Ctx).Info("--- Retrieving Secret " + vol.Secret.SecretName)
					svs := vol.Secret

					secret, err := Clientset.CoreV1().Secrets(pod.Namespace).Get(Ctx, svs.SecretName, metav1.GetOptions{})

					if err != nil {
						log.G(Ctx).Error(err)
						return commonIL.RetrievedContainer{}, err
					} else {
						log.G(Ctx).Debug("---- Retrieved Secret " + svs.SecretName)
					}

					if secret.Data != nil {
						retrieved_data.Name = container.Name
						retrieved_data.Secrets = append(retrieved_data.Secrets, *secret)
					}

				} else if vol.EmptyDir != nil {
					edPath := filepath.Join(commonIL.InterLinkConfigInst.DataRootFolder, pod.Namespace+"-"+string(pod.UID)+"/"+"emptyDirs/"+vol.Name)

					retrieved_data.Name = container.Name
					retrieved_data.EmptyDirs = append(retrieved_data.EmptyDirs, edPath)
				}
			}

		}
	}
	return retrieved_data, nil
}
