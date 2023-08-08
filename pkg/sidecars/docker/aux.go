package docker

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	exec2 "github.com/alexellis/go-execute/pkg/v1"
	"github.com/containerd/containerd/log"
	commonIL "github.com/intertwin-eu/interlink/pkg/common"
	v1 "k8s.io/api/core/v1"
)

var Ctx context.Context

func prepare_mounts(container v1.Container, data []commonIL.RetrievedPodData) string {
	log.G(Ctx).Info("- Preparing mountpoints for " + container.Name)
	mount_data := ""
	pod_name := strings.Split(container.Name, "-")

	if len(pod_name) > 6 {
		pod_name = pod_name[0:6]
	}

	err := os.MkdirAll(commonIL.InterLinkConfigInst.DataRootFolder+strings.Join(pod_name[:len(pod_name)-1], "-"), os.ModePerm)
	if err != nil {
		log.G(Ctx).Error("Can't create directory " + commonIL.InterLinkConfigInst.DataRootFolder + strings.Join(pod_name[:len(pod_name)-1], "-"))
	} else {
		log.G(Ctx).Debug("- Created directory " + commonIL.InterLinkConfigInst.DataRootFolder + strings.Join(pod_name[:len(pod_name)-1], "-"))
	}

	for _, podData := range data {
		for _, cont := range podData.Containers {
			for _, cfgMap := range cont.ConfigMaps {
				if container.Name == cont.Name {
					paths := mountConfigMaps(container, podData.Pod, cfgMap)
					for _, path := range paths {
						mount_data += "-v " + path + " "
					}
				}
			}

			for _, secret := range cont.Secrets {
				if container.Name == cont.Name {
					paths := mountSecrets(container, podData.Pod, secret)
					for _, path := range paths {
						mount_data += "-v " + path + " "
					}
				}
			}

			for _, emptyDir := range cont.EmptyDirs {
				if container.Name == cont.Name {
					path := mountEmptyDir(container, podData.Pod, emptyDir)
					mount_data += "-v " + path + " "
				}
			}
		}
	}

	if last := len(mount_data) - 1; last >= 0 && mount_data[last] == ',' {
		mount_data = mount_data[:last]
	}
	return mount_data
}

func mountConfigMaps(container v1.Container, pod v1.Pod, cfgMap v1.ConfigMap) []string { //returns an array containing mount paths for configMaps
	var configMapNamePaths []string
	wd, _ := os.Getwd()

	if commonIL.InterLinkConfigInst.ExportPodData {
		cmd := []string{"-rf " + wd + "/" + commonIL.InterLinkConfigInst.DataRootFolder + "configMaps"}
		shell := exec2.ExecTask{
			Command: "rm",
			Args:    cmd,
			Shell:   true,
		}

		_, err := shell.Execute()

		if err != nil {
			log.G(Ctx).Error("Unable to delete root folder")
		}

		for _, mountSpec := range container.VolumeMounts {
			var podVolumeSpec *v1.VolumeSource

			for _, vol := range pod.Spec.Volumes {
				if vol.Name == mountSpec.Name {
					podVolumeSpec = &vol.VolumeSource
				}
				if podVolumeSpec != nil && podVolumeSpec.ConfigMap != nil {
					podConfigMapDir := filepath.Join(wd+"/"+commonIL.InterLinkConfigInst.DataRootFolder, pod.Namespace+"-"+string(pod.UID)+"/", "configMaps/", vol.Name)
					mode := os.FileMode(*podVolumeSpec.ConfigMap.DefaultMode)

					if cfgMap.Data != nil {
						for key := range cfgMap.Data {
							path := filepath.Join(wd+podConfigMapDir, key)
							path += (":" + mountSpec.MountPath + "/" + key + " ")
							configMapNamePaths = append(configMapNamePaths, path)
						}
					}

					cmd = []string{"-p " + podConfigMapDir}
					shell = exec2.ExecTask{
						Command: "mkdir",
						Args:    cmd,
						Shell:   true,
					}

					execReturn, _ := shell.Execute()
					if execReturn.Stderr != "" {
						log.G(Ctx).Error(err)
					} else {
						log.G(Ctx).Debug("--- Created folder " + podConfigMapDir)
					}

					log.G(Ctx).Debug("--- Writing ConfigMaps files")
					for k, v := range cfgMap.Data {
						// TODO: Ensure that these files are deleted in failure cases
						fullPath := filepath.Join(podConfigMapDir, k)
						os.WriteFile(fullPath, []byte(v), mode)
						if err != nil {
							log.G(Ctx).Errorf("Could not write ConfigMap file %s", fullPath)
							err = os.Remove(fullPath)
							if err != nil {
								log.G(Ctx).Error("Unable to remove file " + fullPath)
							}
						} else {
							log.G(Ctx).Debug("--- Written ConfigMap file " + fullPath)
						}
					}
				}
			}
		}
	}
	return configMapNamePaths
}

func mountSecrets(container v1.Container, pod v1.Pod, secret v1.Secret) []string { //returns an array containing mount paths for secrets
	var secretNamePaths []string
	wd, _ := os.Getwd()

	if commonIL.InterLinkConfigInst.ExportPodData {
		cmd := []string{"-rf " + wd + "/" + commonIL.InterLinkConfigInst.DataRootFolder + "secrets"}
		shell := exec2.ExecTask{
			Command: "rm",
			Args:    cmd,
			Shell:   true,
		}

		_, err := shell.Execute()

		if err != nil {
			log.G(Ctx).Error("Unable to delete root folder")
		}

		for _, mountSpec := range container.VolumeMounts {
			var podVolumeSpec *v1.VolumeSource

			for _, vol := range pod.Spec.Volumes {
				if vol.Name == mountSpec.Name {
					podVolumeSpec = &vol.VolumeSource
				}
				if podVolumeSpec != nil && podVolumeSpec.Secret != nil {
					mode := os.FileMode(*podVolumeSpec.Secret.DefaultMode)
					podSecretDir := filepath.Join(wd+"/"+commonIL.InterLinkConfigInst.DataRootFolder, pod.Namespace+"-"+string(pod.UID)+"/", "secrets/", vol.Name)

					if secret.Data != nil {
						for key := range secret.Data {
							path := filepath.Join(podSecretDir, key)
							path += (":" + mountSpec.MountPath + "/" + key + " ")
							secretNamePaths = append(secretNamePaths, path)
						}
					}

					cmd = []string{"-p " + podSecretDir}
					shell = exec2.ExecTask{
						Command: "mkdir",
						Args:    cmd,
						Shell:   true,
					}

					execReturn, _ := shell.Execute()
					if strings.Compare(execReturn.Stdout, "") != 0 {
						log.G(Ctx).Error(err)
					}
					if execReturn.Stderr != "" {
						log.G(Ctx).Error(err)
					} else {
						log.G(Ctx).Debug("--- Created folder " + podSecretDir)
					}

					log.G(Ctx).Debug("--- Writing Secret files")
					for k, v := range secret.Data {
						// TODO: Ensure that these files are deleted in failure cases
						fullPath := filepath.Join(podSecretDir, k)
						os.WriteFile(fullPath, v, mode)
						if err != nil {
							log.G(Ctx).Errorf("Could not write Secret file %s", fullPath)
							err = os.Remove(fullPath)
							if err != nil {
								log.G(Ctx).Error("Unable to remove file " + fullPath)
							}
						} else {
							log.G(Ctx).Debug("--- Written Secret file " + fullPath)
						}
					}
				}
			}
		}
	}
	return secretNamePaths
}

func mountEmptyDir(container v1.Container, pod v1.Pod, emptyDir string) string {
	var edPath string
	wd, _ := os.Getwd()

	if commonIL.InterLinkConfigInst.ExportPodData {
		cmd := []string{"-rf " + wd + "/" + commonIL.InterLinkConfigInst.DataRootFolder + "emptyDirs"}
		shell := exec2.ExecTask{
			Command: "rm",
			Args:    cmd,
			Shell:   true,
		}

		_, err := shell.Execute()

		if err != nil {
			log.G(Ctx).Error("Unable to delete root folder")
		}

		for _, mountSpec := range container.VolumeMounts {
			var podVolumeSpec *v1.VolumeSource

			for _, vol := range pod.Spec.Volumes {
				if vol.Name == mountSpec.Name {
					podVolumeSpec = &vol.VolumeSource
				}
				if podVolumeSpec != nil && podVolumeSpec.EmptyDir != nil {
					edPath = filepath.Join(wd+"/"+commonIL.InterLinkConfigInst.DataRootFolder, pod.Namespace+"-"+string(pod.UID)+"/"+"emptyDirs/"+vol.Name)
					log.G(Ctx).Info("-- Creating EmptyDir in " + edPath)
					cmd := []string{"-p " + edPath}
					shell := exec2.ExecTask{
						Command: "mkdir",
						Args:    cmd,
						Shell:   true,
					}

					_, err := shell.Execute()
					if err != nil {
						log.G(Ctx).Error(err)
					} else {
						log.G(Ctx).Debug("-- Created EmptyDir in " + edPath)
					}

					edPath += (":" + mountSpec.MountPath + "/" + mountSpec.Name + " ")
				}
			}
		}
	}
	return edPath
}
