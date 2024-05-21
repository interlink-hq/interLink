package api

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/containerd/containerd/log"
	"gopkg.in/yaml.v3"
	v1 "k8s.io/api/core/v1"

	commonIL "github.com/intertwin-eu/interlink/pkg/interlink"
)

// MutexDeletePods is a struct holding a mutex and a map of all deleted pods. It used to avoid cache recreation even after a pod is deleted
type MutexDeletePods struct {
	mu   sync.Mutex
	Pods map[string]time.Time
}

// Init creates a map holding deleted pods and their deletion timestamps. It also starts a goroutine to periodically check pods to delete (older than 1h)
func (p *MutexDeletePods) Init() {
	p.Pods = make(map[string]time.Time)
	go p.DeleteOlderThan()
}

// Add adds deleted the pods' deletion timestamps to the p.Pods map indexing them through their UID
func (p *MutexDeletePods) Add(uid string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Pods[uid] = time.Now()
}

// Delete synchronusly removes a deletion timestamp using the pod UID to index it
func (p *MutexDeletePods) Delete(uid string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.Pods, uid)
}

// DeleteOlderThan is thought to be used as a goroutine indefinetely running.
// Every 1m, it checks for entries older than 1h and removes them calling the Delete function.
// it runs an endless for loop to avoid exiting
func (p *MutexDeletePods) DeleteOlderThan() {
	for {
		olderThan := time.Now()
		p.mu.Lock()
		for key, value := range p.Pods {
			if olderThan.Sub(value) >= time.Hour {
				p.Delete(key)
			}
		}
		p.mu.Unlock()
		time.Sleep(time.Minute)
	}
}

// MutexStatuses holds a mutex for synchronous access to the Statuses map, which holds all necessary values to create a cache for interlink
type MutexStatuses struct {
	mu       sync.Mutex
	Statuses map[string]commonIL.PodStatus
}

// Init creates the Statuses map
func (s *MutexStatuses) Init() {
	s.Statuses = make(map[string]commonIL.PodStatus)
}

// Delete synchronusly removes a key/value pair from the Statuses map using the pod UID to index it
func (s *MutexStatuses) Delete(uid string) {
	s.mu.Lock()
	delete(s.Statuses, uid)
	s.mu.Unlock()
}

// Add adds a key/value pair to the Statuses map. It uses the pod UID as key and a commonIL.PodStatus as value.
func (s *MutexStatuses) Add(status commonIL.PodStatus) {
	s.mu.Lock()
	s.Statuses[status.PodUID] = status
	s.mu.Unlock()
}

var PodStatuses MutexStatuses
var DeletedPods MutexDeletePods

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
// It also deletes the $rootDir/.cache/podUID.yaml
func deleteCachedStatus(config commonIL.InterLinkConfig, uid string) error {
	PodStatuses.Delete(uid)
	DeletedPods.Add(uid)
	os.Remove(config.DataRootFolder + ".cache/" + uid + ".yaml")
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

// checkIfDeleted checks if the uid key is present in the DeletedPods map and returns a bool
func checkIfDeleted(uid string) bool {
	_, ok := DeletedPods.Pods[uid]

	if ok {
		return true
	} else {
		return false
	}
}

// updateStatuses locks and updates the PodStatuses map with the statuses contained in the returnedStatuses slice.
// It also writes the yaml status for each pod into $RootDir/.cache/podUID.yaml upon checking for storage availability
func updateStatuses(config commonIL.InterLinkConfig, returnedStatuses []commonIL.PodStatus) error {
	for _, new := range returnedStatuses {
		if !checkIfDeleted(new.PodUID) {
			_, err := os.Stat(config.DataRootFolder + ".cache")
			if err != nil {
				err = os.MkdirAll(config.DataRootFolder+".cache", os.ModePerm)
				if err != nil {
					return err
				}
			}

			dirSize, err := dirSize(config.DataRootFolder + ".cache/")
			dirSize = dirSize / 1024 / 1024
			if err != nil {
				return err
			}

			if dirSize > config.InterlinkCacheSize {
				log.L.Fatal("The cache is full")
			}

			statusBytes, err := yaml.Marshal(new)
			if err != nil {
				return err
			}
			err = os.WriteFile(config.DataRootFolder+".cache/"+new.PodUID+".yaml", statusBytes, fs.ModePerm)
			if err != nil {
				return err
			}

			PodStatuses.Add(new)
		} else {
			deleteCachedStatus(config, new.PodUID)
		}
	}

	return nil
}

// LoadCache reads all entries inside $RootDir/.cache and attempts to load YAMLs to restore the cache
func LoadCache(ctx context.Context, config commonIL.InterLinkConfig) error {
	dirPath := config.DataRootFolder
	counterPods := 0

	_, err := os.Stat(dirPath + "/.cache")
	if err != nil {
		err := os.MkdirAll(dirPath+"/.cache", fs.ModePerm)
		if err != nil {
			log.G(ctx).Error("Unable to create directory " + dirPath)
			return err
		}
	}
	dirs, err := os.ReadDir(dirPath + "/.cache")
	if err != nil {
		return err
	}
	for _, entry := range dirs {
		var cachedPodStatus commonIL.PodStatus
		file, err := os.ReadFile(dirPath + "/.cache/" + entry.Name())
		if err != nil {
			return err
		}
		err = yaml.Unmarshal(file, &cachedPodStatus)
		if err != nil {
			log.G(ctx).Error("Unable to unmarshal cached pod " + entry.Name())
			return err
		}

		PodStatuses.Add(cachedPodStatus)
		counterPods++
	}

	log.G(ctx).Info("Restored " + fmt.Sprintf("%d", counterPods) + " cached files from disk")

	log.G(ctx).Info(PodStatuses.Statuses)
	return err
}

// DirSize retrieve the whole directory size and returns it in bytes
func dirSize(path string) (int64, error) {
	var size int64
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return err
	})
	return size, err
}
