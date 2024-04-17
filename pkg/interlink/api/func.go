package api

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/containerd/containerd/log"
	"gopkg.in/yaml.v3"
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

// deleteCachedStatus locks the map PodStatuses and delete the uid key from that map.
// It also deletes the $rootDir/cachedStatuses/podUID.yaml cache file
func deleteCachedStatus(config commonIL.InterLinkConfig, uid string) error {
	PodStatuses.mu.Lock()
	delete(PodStatuses.Statuses, uid)
	PodStatuses.mu.Unlock()
	err := os.Remove(config.DataRootFolder + "/cachedStatuses/" + uid + ".yaml")
	if err != nil {
		return err
	}
	return nil
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

// updateStatuses locks and updates the PodStatuses map with the statuses contained in the returnedStatuses slice.
// It also writes the yaml status for each pod into $RootDir/cachedStatuses/podUID.yaml
func updateStatuses(config commonIL.InterLinkConfig, returnedStatuses []commonIL.PodStatus) error {
	PodStatuses.mu.Lock()

	for _, new := range returnedStatuses {
		statusBytes, err := yaml.Marshal(new)
		if err != nil {
			return err
		}

		//TBD: check the size before writing. If the size is too big, replace the oldest

		err = os.WriteFile(config.DataRootFolder+"/cachedStatuses/"+new.PodUID+".yaml", statusBytes, fs.ModePerm)
		if err != nil {
			return err
		}
		//log.G(ctx).Debug(PodStatuses.Statuses, new)
		PodStatuses.Statuses[new.PodUID] = new
	}

	PodStatuses.mu.Unlock()
	return nil
}

// LoadCache reads all entries inside $RootDir/cachedStatuses and attempts to load YAMLs to restore the cache
func LoadCache(ctx context.Context, config commonIL.InterLinkConfig) error {
	dirs, err := os.ReadDir(config.DataRootFolder + "/cachedStatuses")
	if err != nil {
		return err
	}

	PodStatuses.mu.Lock()
	for _, entry := range dirs {
		var cachedPodStatus commonIL.PodStatus
		file, err := os.ReadFile(entry.Name())
		if err != nil {
			log.G(ctx).Error("Unable to read " + entry.Name())
			return err
		}
		err = yaml.Unmarshal(file, &cachedPodStatus)
		if err != nil {
			log.G(ctx).Error("Unable to unmarshal cached pod " + entry.Name())
			return err
		}

		PodStatuses.Statuses[cachedPodStatus.PodUID] = cachedPodStatus

	}
	PodStatuses.mu.Unlock()

	return nil
}
