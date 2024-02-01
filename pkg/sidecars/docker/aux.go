package docker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	exec2 "github.com/alexellis/go-execute/pkg/v1"
	"github.com/containerd/containerd/log"
	v1 "k8s.io/api/core/v1"

	commonIL "github.com/intertwin-eu/interlink/pkg/common"
)

type SidecarHandler struct {
	Config commonIL.InterLinkConfig
	Ctx    context.Context
}

func prepareMounts(container v1.Container, data []commonIL.RetrievedPodData, config commonIL.InterLinkConfig, Ctx context.Context) (string, error) {
	log.G(Ctx).Info("- Preparing mountpoints for " + container.Name)
	mountedData := ""

	for _, podData := range data {
		err := os.MkdirAll(config.DataRootFolder+podData.Pod.Namespace+"-"+string(podData.Pod.UID), os.ModePerm)
		if err != nil {
			log.G(Ctx).Error(err)
			return "", err
		} else {
			log.G(Ctx).Info("-- Created directory " + config.DataRootFolder + podData.Pod.Namespace + "-" + string(podData.Pod.UID))
		}
		for _, cont := range podData.Containers {
			for _, cfgMap := range cont.ConfigMaps {
				if container.Name == cont.Name {
					paths, err := mountData(container, podData.Pod, cfgMap, config, Ctx)
					if err != nil {
						log.G(Ctx).Error("Error mounting ConfigMap " + cfgMap.Name)
						return "", errors.New("Error mounting ConfigMap " + cfgMap.Name)
					}
					for _, path := range paths {
						mountedData += "-v " + path + " "
					}
				}
			}

			for _, secret := range cont.Secrets {
				if container.Name == cont.Name {
					paths, err := mountData(container, podData.Pod, secret, config, Ctx)
					if err != nil {
						log.G(Ctx).Error("Error mounting Secret " + secret.Name)
						return "", errors.New("Error mounting Secret " + secret.Name)
					}
					for _, path := range paths {
						mountedData += "-v " + path + " "
					}
				}
			}

			for _, emptyDir := range cont.EmptyDirs {
				if container.Name == cont.Name {
					paths, err := mountData(container, podData.Pod, emptyDir, config, Ctx)
					if err != nil {
						log.G(Ctx).Error("Error mounting EmptyDir " + emptyDir)
						return "", errors.New("Error mounting EmptyDir " + emptyDir)
					}
					for _, path := range paths {
						mountedData += "-v " + path + " "
					}
				}
			}
		}
	}

	if last := len(mountedData) - 1; last >= 0 && mountedData[last] == ',' {
		mountedData = mountedData[:last]
	}
	return mountedData, nil
}

func mountData(container v1.Container, pod v1.Pod, data interface{}, config commonIL.InterLinkConfig, Ctx context.Context) ([]string, error) {
	wd, err := os.Getwd()
	if err != nil {
		log.G(Ctx).Error(err)
		return nil, err
	}

	if config.ExportPodData {
		for _, mountSpec := range container.VolumeMounts {
			var podVolumeSpec *v1.VolumeSource

			for _, vol := range pod.Spec.Volumes {
				if vol.Name == mountSpec.Name {
					podVolumeSpec = &vol.VolumeSource
				}

				switch mount := data.(type) {
				case v1.ConfigMap:
					var configMapNamePaths []string
					err := os.RemoveAll(config.DataRootFolder + string(pod.UID) + "/" + "configMaps/" + vol.Name)

					if err != nil {
						log.G(Ctx).Error("Unable to delete root folder")
						return nil, err
					}
					if podVolumeSpec != nil && podVolumeSpec.ConfigMap != nil {
						podConfigMapDir := filepath.Join(wd+"/"+config.DataRootFolder, string(pod.UID)+"/", "configMaps/", vol.Name)
						mode := os.FileMode(*podVolumeSpec.ConfigMap.DefaultMode)

						if mount.Data != nil {
							for key := range mount.Data {
								path := filepath.Join(wd+podConfigMapDir, key)
								path += (":" + mountSpec.MountPath + "/" + key + " ")
								configMapNamePaths = append(configMapNamePaths, path)
							}
						}

						cmd := []string{"-p " + podConfigMapDir}
						shell := exec2.ExecTask{
							Command: "mkdir",
							Args:    cmd,
							Shell:   true,
						}

						execReturn, _ := shell.Execute()
						if execReturn.Stderr != "" {
							log.G(Ctx).Error(err)
							return nil, err
						} else {
							log.G(Ctx).Debug("-- Created directory " + podConfigMapDir)
						}

						log.G(Ctx).Info("-- Writing ConfigMaps files")
						for k, v := range mount.Data {
							// TODO: Ensure that these files are deleted in failure cases
							fullPath := filepath.Join(podConfigMapDir, k)
							os.WriteFile(fullPath, []byte(v), mode)
							if err != nil {
								log.G(Ctx).Errorf("Could not write ConfigMap file %s", fullPath)
								err = os.RemoveAll(fullPath)
								if err != nil {
									log.G(Ctx).Error("Unable to remove file " + fullPath)
								}
								return nil, err
							} else {
								log.G(Ctx).Debug("--- Written ConfigMap file " + fullPath)
							}
						}
						return configMapNamePaths, nil
					}

				case v1.Secret:
					var secretNamePaths []string
					err := os.RemoveAll(config.DataRootFolder + string(pod.UID) + "/" + "secrets/" + vol.Name)

					if err != nil {
						log.G(Ctx).Error("Unable to delete root folder")
						return nil, err
					}
					if podVolumeSpec != nil && podVolumeSpec.Secret != nil {
						mode := os.FileMode(*podVolumeSpec.Secret.DefaultMode)
						podSecretDir := filepath.Join(wd+"/"+config.DataRootFolder, string(pod.UID)+"/", "secrets/", vol.Name)

						if mount.Data != nil {
							for key := range mount.Data {
								path := filepath.Join(podSecretDir, key)
								path += (":" + mountSpec.MountPath + "/" + key + " ")
								secretNamePaths = append(secretNamePaths, path)
							}
						}

						cmd := []string{"-p " + podSecretDir}
						shell := exec2.ExecTask{
							Command: "mkdir",
							Args:    cmd,
							Shell:   true,
						}

						execReturn, _ := shell.Execute()
						if strings.Compare(execReturn.Stdout, "") != 0 {
							log.G(Ctx).Error(err)
							return nil, err
						}
						if execReturn.Stderr != "" {
							log.G(Ctx).Error(execReturn.Stderr)
							return nil, errors.New(execReturn.Stderr)
						} else {
							log.G(Ctx).Debug("-- Created directory " + podSecretDir)
						}

						log.G(Ctx).Info("-- Writing Secret files")
						for k, v := range mount.Data {
							// TODO: Ensure that these files are deleted in failure cases
							fullPath := filepath.Join(podSecretDir, k)
							os.WriteFile(fullPath, v, mode)
							if err != nil {
								log.G(Ctx).Errorf("Could not write Secret file %s", fullPath)
								err = os.RemoveAll(fullPath)
								if err != nil {
									log.G(Ctx).Error("Unable to remove file " + fullPath)
								}
								return nil, err
							} else {
								log.G(Ctx).Debug("--- Written Secret file " + fullPath)
							}
						}
						return secretNamePaths, nil
					}

				case string:
					if podVolumeSpec != nil && podVolumeSpec.EmptyDir != nil {
						var edPath string

						edPath = filepath.Join(wd+"/"+config.DataRootFolder, string(pod.UID)+"/"+"emptyDirs/"+vol.Name)
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
							return []string{""}, nil
						} else {
							log.G(Ctx).Debug("-- Created EmptyDir in " + edPath)
						}

						edPath += (":" + mountSpec.MountPath + "/" + mountSpec.Name + " ")
						return []string{edPath}, nil
					}
				}
			}
		}
	}
	return nil, err
}
