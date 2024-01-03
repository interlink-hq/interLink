package slurm

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	exec2 "github.com/alexellis/go-execute/pkg/v1"
	"github.com/containerd/containerd/log"
	commonIL "github.com/intertwin-eu/interlink/pkg/common"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var prefix string
var Ctx context.Context
var JIDs []JidStruct
var timer time.Time
var cachedStatus []commonIL.PodStatus

type JidStruct struct {
	PodUID    string    `json:"PodUID"`
	JID       string    `json:"JID"`
	StartTime time.Time `json:"StartTime"`
	EndTime   time.Time `json:"EndTime"`
}

type SingularityCommand struct {
	containerName string
	command       []string
}

func parsingTimeFromString(stringTime string) (time.Time, error) {
	parsedTime := time.Time{}
	timestampFormat := "2006-01-02 15:04:05.999999999 -0700 MST"
	parts := strings.Fields(stringTime)
	if len(parts) != 4 {
		err := errors.New("Invalid timestamp format")
		log.G(Ctx).Error(err)
		return time.Time{}, err
	}

	parsedTime, err := time.Parse(timestampFormat, stringTime)
	if err != nil {
		log.G(Ctx).Error(err)
		return time.Time{}, err
	}

	if err != nil {
		log.G(Ctx).Error(err)
		return time.Time{}, err
	}

	return parsedTime, nil
}

func CreateDirectories() error {
	path := commonIL.InterLinkConfigInst.DataRootFolder
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			err = os.MkdirAll(path, os.ModePerm)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func Load_JIDs() error {
	path := commonIL.InterLinkConfigInst.DataRootFolder

	dir, err := os.Open(path)
	if err != nil {
		log.G(Ctx).Error(err)
		return err
	}
	defer dir.Close()

	entries, err := dir.ReadDir(0)
	if err != nil {
		log.G(Ctx).Error(err)
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			podUID := entry.Name()
			StartedAt := time.Time{}
			FinishedAt := time.Time{}
			JID, err := os.ReadFile(path + entry.Name() + "/" + "JobID.jid")
			if err != nil {
				log.G(Ctx).Error(err)
				return err
			}

			StartedAtString, err := os.ReadFile(path + entry.Name() + "/" + "StartedAt.time")
			if err != nil {
				log.G(Ctx).Debug(err)
			} else {
				StartedAt, err = parsingTimeFromString(string(StartedAtString))
				if err != nil {
					log.G(Ctx).Debug(err)
				}
			}

			FinishedAtString, err := os.ReadFile(path + entry.Name() + "/" + "FinishedAt.time")
			if err != nil {
				log.G(Ctx).Debug(err)
			} else {
				FinishedAt, err = parsingTimeFromString(string(FinishedAtString))
				if err != nil {
					log.G(Ctx).Debug(err)
				}
			}

			JIDs = append(JIDs, JidStruct{PodUID: podUID, JID: string(JID), StartTime: StartedAt, EndTime: FinishedAt})
		}
	}

	log.G(Ctx).Debug(JIDs)

	return nil
}

func prepare_envs(container v1.Container) []string {
	if len(container.Env) > 0 {
		log.G(Ctx).Info("-- Appending envs")
		env := make([]string, 1)
		env = append(env, "--env")
		env_data := ""
		for _, env_var := range container.Env {
			tmp := (env_var.Name + "=" + env_var.Value + ",")
			env_data += tmp
		}
		if last := len(env_data) - 1; last >= 0 && env_data[last] == ',' {
			env_data = env_data[:last]
		}
		if env_data == "" {
			env = []string{}
		}
		env = append(env, env_data)

		return env
	} else {
		return []string{}
	}
}

func prepare_mounts(container v1.Container, data []commonIL.RetrievedPodData) ([]string, error) {
	log.G(Ctx).Info("-- Preparing mountpoints for " + container.Name)
	mount := make([]string, 1)
	mount = append(mount, "--bind")
	mount_data := ""

	for _, podData := range data {
		err := os.MkdirAll(commonIL.InterLinkConfigInst.DataRootFolder+string(podData.Pod.UID), os.ModePerm)
		if err != nil {
			log.G(Ctx).Error(err)
			return nil, err
		} else {
			log.G(Ctx).Info("-- Created directory " + commonIL.InterLinkConfigInst.DataRootFolder + string(podData.Pod.UID))
		}

		for _, cont := range podData.Containers {
			for _, cfgMap := range cont.ConfigMaps {
				if container.Name == cont.Name {
					configMapsPaths, envs, err := mountData(container, podData.Pod, cfgMap)
					if err != nil {
						log.G(Ctx).Error(err)
						return nil, err
					}

					for i, path := range configMapsPaths {
						if os.Getenv("SHARED_FS") != "true" {
							dirs := strings.Split(path, ":")
							splitDirs := strings.Split(dirs[0], "/")
							dir := filepath.Join(splitDirs[:len(splitDirs)-1]...)
							prefix += "\nmkdir -p " + dir + " && touch " + dirs[0] + " && echo $" + envs[i] + " > " + dirs[0]
							mount_data += dir
						} else {
							mount_data += path
						}
					}
				}
			}

			for _, secret := range cont.Secrets {
				if container.Name == cont.Name {
					secretsPaths, envs, err := mountData(container, podData.Pod, secret)
					if err != nil {
						log.G(Ctx).Error(err)
						return nil, err
					}
					for i, path := range secretsPaths {
						if os.Getenv("SHARED_FS") != "true" {
							dirs := strings.Split(path, ":")
							splitDirs := strings.Split(dirs[0], "/")
							dir := filepath.Join(splitDirs[:len(splitDirs)-1]...)
							prefix += "\nmkdir -p " + dir + " && touch " + dirs[0] + " && echo $" + envs[i] + " > " + dirs[0]
							mount_data += dir
						} else {
							mount_data += path
						}
					}
				}
			}

			for _, emptyDir := range cont.EmptyDirs {
				if container.Name == cont.Name {
					paths, _, err := mountData(container, podData.Pod, emptyDir)
					if err != nil {
						log.G(Ctx).Error(err)
						return nil, err
					}
					for _, path := range paths {
						mount_data += path
					}
				}
			}
		}
	}

	//path_hardcoded := ("/cvmfs/grid.cern.ch/etc/grid-security:/etc/grid-security" + "," +
	//	"/cvmfs:/cvmfs" + ",")
	//mount_data += path_hardcoded
	if last := len(mount_data) - 1; last >= 0 && mount_data[last] == ',' {
		mount_data = mount_data[:last]
	}
	if len(mount_data) == 0 {
		return []string{}, nil
	}
	return append(mount, mount_data), nil
}

func produce_slurm_script(podUID string, metadata metav1.ObjectMeta, commands []SingularityCommand) (string, error) {
	log.G(Ctx).Info("-- Creating file for the Slurm script")
	err := os.MkdirAll(commonIL.InterLinkConfigInst.DataRootFolder+podUID, os.ModePerm)
	if err != nil {
		log.G(Ctx).Error(err)
		return "", err
	} else {
		log.G(Ctx).Info("-- Created directory " + commonIL.InterLinkConfigInst.DataRootFolder + podUID)
	}
	path := commonIL.InterLinkConfigInst.DataRootFolder + podUID + "/job.sh"
	postfix := ""

	err = os.RemoveAll(path)
	if err != nil {
		log.G(Ctx).Error(err)
		return "", err
	}
	f, err := os.Create(path)
	if err != nil {
		log.G(Ctx).Error(err)
		return "", err
	}
	err = os.Chmod(path, 0774)
	if err != nil {
		log.G(Ctx).Error(err)
		return "", err
	}
	defer f.Close()

	if err != nil {
		log.G(Ctx).Error("Unable to create file " + path)
		return "", err
	} else {
		log.G(Ctx).Debug("--- Created file " + path)
	}

	var sbatch_flags_from_argo []string
	var sbatch_flags_as_string = ""
	if slurm_flags, ok := metadata.Annotations["slurm-job.vk.io/flags"]; ok {
		sbatch_flags_from_argo = strings.Split(slurm_flags, " ")
	}
	if mpi_flags, ok := metadata.Annotations["slurm-job.vk.io/mpi-flags"]; ok {
		if mpi_flags != "true" {
			mpi := append([]string{"mpiexec", "-np", "$SLURM_NTASKS"}, strings.Split(mpi_flags, " ")...)
			for _, singularityCommand := range commands {
				singularityCommand.command = append(mpi, singularityCommand.command...)
			}
		}
	}

	for _, slurm_flag := range sbatch_flags_from_argo {
		sbatch_flags_as_string += "\n#SBATCH " + slurm_flag
	}

	if commonIL.InterLinkConfigInst.Tsocks {
		log.G(Ctx).Debug("--- Adding SSH connection and setting ENVs to use TSOCKS")
		postfix += "\n\nkill -15 $SSH_PID &> log2.txt"

		prefix += "\n\nmin_port=10000"
		prefix += "\nmax_port=65000"
		prefix += "\nfor ((port=$min_port; port<=$max_port; port++))"
		prefix += "\ndo"
		prefix += "\n  temp=$(ss -tulpn | grep :$port)"
		prefix += "\n  if [ -z \"$temp\" ]"
		prefix += "\n  then"
		prefix += "\n    break"
		prefix += "\n  fi"
		prefix += "\ndone"

		prefix += "\nssh -4 -N -D $port " + commonIL.InterLinkConfigInst.Tsockslogin + " &"
		prefix += "\nSSH_PID=$!"
		prefix += "\necho \"local = 10.0.0.0/255.0.0.0 \nserver = 127.0.0.1 \nserver_port = $port\" >> .tmp/" + podUID + "_tsocks.conf"
		prefix += "\nexport TSOCKS_CONF_FILE=.tmp/" + podUID + "_tsocks.conf && export LD_PRELOAD=" + commonIL.InterLinkConfigInst.Tsockspath
	}

	if commonIL.InterLinkConfigInst.Commandprefix != "" {
		prefix += "\n" + commonIL.InterLinkConfigInst.Commandprefix
	}

	if preExecAnnotations, ok := metadata.Annotations["job.vk.io/pre-exec"]; ok {
		prefix += "\n" + preExecAnnotations
	}

	sbatch_macros := "#!" + commonIL.InterLinkConfigInst.BashPath +
		"\n#SBATCH --job-name=" + podUID +
		"\n#SBATCH --output=" + commonIL.InterLinkConfigInst.DataRootFolder + podUID + "/" + "job.out" +
		sbatch_flags_as_string +
		"\n" +
		prefix +
		"\n"

	log.G(Ctx).Debug("--- Writing file")

	var stringToBeWritten string

	stringToBeWritten += sbatch_macros

	for _, singularityCommand := range commands {
		stringToBeWritten += "\n" + strings.Join(singularityCommand.command[:], " ") +
			" &> " + commonIL.InterLinkConfigInst.DataRootFolder + podUID + "/" + singularityCommand.containerName + ".out; " +
			"echo $? > " + commonIL.InterLinkConfigInst.DataRootFolder + podUID + "/" + singularityCommand.containerName + ".status &"
	}

	stringToBeWritten += "\n" + postfix

	_, err = f.WriteString(stringToBeWritten)

	if err != nil {
		log.G(Ctx).Error(err)
		return "", err
	} else {
		log.G(Ctx).Debug("---- Written file")
	}

	return path, nil
}

func slurm_batch_submit(path string) (string, error) {
	log.G(Ctx).Info("- Submitting Slurm job")
	cmd := []string{path}
	shell := exec2.ExecTask{
		Command: commonIL.InterLinkConfigInst.Sbatchpath,
		Args:    cmd,
		Shell:   true,
	}

	execReturn, err := shell.Execute()
	if err != nil {
		log.G(Ctx).Error("Unable to create file " + path)
		return "", err
	}
	execReturn.Stdout = strings.ReplaceAll(execReturn.Stdout, "\n", "")

	if execReturn.Stderr != "" {
		log.G(Ctx).Error("Could not run sbatch: " + execReturn.Stderr)
		return "", errors.New(execReturn.Stderr)
	} else {
		log.G(Ctx).Debug("Job submitted")
	}
	return string(execReturn.Stdout), nil
}

func handle_jid(podUID string, output string, pod v1.Pod) error {
	//Submitted batch job 8017236
	//Submitted batch job 60920
	r := regexp.MustCompile(`Submitted batch job (?P<jid>\d+)`)
	jid := r.FindStringSubmatch(output)
	f, err := os.Create(commonIL.InterLinkConfigInst.DataRootFolder + podUID + "/JobID.jid")
	if err != nil {
		log.G(Ctx).Error("Can't create jid_file")
		return err
	}
	_, err = f.WriteString(jid[1])
	f.Close()
	if err != nil {
		log.G(Ctx).Error(err)
		return err
	}
	JIDs = append(JIDs, JidStruct{PodUID: string(pod.UID), JID: string(jid[1])})
	log.G(Ctx).Info("Job ID is: " + jid[1])
	return nil
}

func removeJID(jidToBeRemoved string) {
	for i, jid := range JIDs {
		if jid.JID == jidToBeRemoved {
			if len(JIDs) == 1 {
				JIDs = nil
			} else if i == 0 {
				JIDs = JIDs[1:]
			} else if i == len(JIDs)-1 {
				JIDs = JIDs[:i]
			} else {
				JIDs = append(JIDs[:i-1], JIDs[i+1:]...)
				return
			}
		}
	}
}

func delete_container(podUID string) error {
	log.G(Ctx).Info("- Deleting Job for pod " + podUID)
	for _, jid := range JIDs {
		if jid.PodUID == podUID {
			_, err := exec.Command(commonIL.InterLinkConfigInst.Scancelpath, jid.JID).Output()
			if err != nil {
				log.G(Ctx).Error(err)
				return err
			} else {
				log.G(Ctx).Info("- Deleted Job ", jid.JID)
			}
			os.RemoveAll(commonIL.InterLinkConfigInst.DataRootFolder + podUID)
			removeJID(jid.JID)
			return nil
		}
	}
	log.G(Ctx).Error("JID for deletion does not exist anymore")
	return nil
}

func mountData(container v1.Container, pod v1.Pod, data interface{}) ([]string, []string, error) {
	wd, err := os.Getwd()
	if err != nil {
		log.G(Ctx).Error(err)
		return nil, nil, err
	}

	if commonIL.InterLinkConfigInst.ExportPodData {
		for _, mountSpec := range container.VolumeMounts {
			var podVolumeSpec *v1.VolumeSource

			for _, vol := range pod.Spec.Volumes {
				if vol.Name == mountSpec.Name {
					podVolumeSpec = &vol.VolumeSource
				}

				switch mount := data.(type) {
				case v1.ConfigMap:
					configMaps := make(map[string]string)
					var configMapNamePaths []string
					var envs []string

					err := os.RemoveAll(commonIL.InterLinkConfigInst.DataRootFolder + pod.Namespace + "-" + string(pod.UID) + "/" + "configMaps/" + vol.Name)

					if err != nil {
						log.G(Ctx).Error("Unable to delete root folder")
						return nil, nil, err
					}

					if podVolumeSpec != nil && podVolumeSpec.ConfigMap != nil {
						log.G(Ctx).Info("--- Mounting ConfigMap " + podVolumeSpec.ConfigMap.Name)
						mode := os.FileMode(*podVolumeSpec.ConfigMap.DefaultMode)
						podConfigMapDir := filepath.Join(commonIL.InterLinkConfigInst.DataRootFolder, pod.Namespace+"-"+string(pod.UID)+"/", "configMaps/", vol.Name)

						if mount.Data != nil {
							for key := range mount.Data {
								configMaps[key] = mount.Data[key]
								path := filepath.Join(podConfigMapDir, key)
								path += (":" + mountSpec.MountPath + "/" + key + ",")
								configMapNamePaths = append(configMapNamePaths, path)

								if os.Getenv("SHARED_FS") != "true" {
									env := string(container.Name) + "_CFG_" + key
									log.G(Ctx).Debug("---- Setting env " + env + " to mount the file later")
									os.Setenv(env, mount.Data[key])
									envs = append(envs, env)
								}
							}
						}

						if os.Getenv("SHARED_FS") == "true" {
							log.G(Ctx).Info("--- Shared FS enabled, files will be directly created before the job submission")
							cmd := []string{"-p " + podConfigMapDir}
							shell := exec2.ExecTask{
								Command: "mkdir",
								Args:    cmd,
								Shell:   true,
							}

							execReturn, err := shell.Execute()

							if err != nil {
								log.G(Ctx).Error(err)
								return nil, nil, err
							} else if execReturn.Stderr != "" {
								log.G(Ctx).Error(execReturn.Stderr)
								return nil, nil, errors.New(execReturn.Stderr)
							} else {
								log.G(Ctx).Debug("--- Created folder " + podConfigMapDir)
							}

							log.G(Ctx).Debug("--- Writing ConfigMaps files")
							for k, v := range configMaps {
								// TODO: Ensure that these files are deleted in failure cases
								fullPath := filepath.Join(podConfigMapDir, k)
								err = os.WriteFile(fullPath, []byte(v), mode)
								if err != nil {
									log.G(Ctx).Errorf("Could not write ConfigMap file %s", fullPath)
									os.RemoveAll(fullPath)
									if err != nil {
										log.G(Ctx).Error("Unable to remove file " + fullPath)
										return nil, nil, err
									}
									return nil, nil, err
								} else {
									log.G(Ctx).Debug("Written ConfigMap file " + fullPath)
								}
							}
						}
						return configMapNamePaths, envs, nil
					}

				case v1.Secret:
					secrets := make(map[string][]byte)
					var secretNamePaths []string
					var envs []string

					err := os.RemoveAll(commonIL.InterLinkConfigInst.DataRootFolder + pod.Namespace + "-" + string(pod.UID) + "/" + "secrets/" + vol.Name)

					if err != nil {
						log.G(Ctx).Error("Unable to delete root folder")
						return nil, nil, err
					}

					if podVolumeSpec != nil && podVolumeSpec.Secret != nil {
						log.G(Ctx).Info("--- Mounting Secret " + podVolumeSpec.Secret.SecretName)
						mode := os.FileMode(*podVolumeSpec.Secret.DefaultMode)
						podSecretDir := filepath.Join(commonIL.InterLinkConfigInst.DataRootFolder, pod.Namespace+"-"+string(pod.UID)+"/", "secrets/", vol.Name)

						if mount.Data != nil {
							for key := range mount.Data {
								secrets[key] = mount.Data[key]
								path := filepath.Join(podSecretDir, key)
								path += (":" + mountSpec.MountPath + "/" + key + ",")
								secretNamePaths = append(secretNamePaths, path)

								if os.Getenv("SHARED_FS") != "true" {
									env := string(container.Name) + "_SECRET_" + key
									log.G(Ctx).Debug("---- Setting env " + env + " to mount the file later")
									os.Setenv(env, string(mount.Data[key]))
									envs = append(envs, env)
								}
							}
						}

						if os.Getenv("SHARED_FS") == "true" {
							log.G(Ctx).Info("--- Shared FS enabled, files will be directly created before the job submission")
							cmd := []string{"-p " + podSecretDir}
							shell := exec2.ExecTask{
								Command: "mkdir",
								Args:    cmd,
								Shell:   true,
							}

							execReturn, err := shell.Execute()
							if strings.Compare(execReturn.Stdout, "") != 0 {
								log.G(Ctx).Error(err)
								return nil, nil, err
							}
							if execReturn.Stderr != "" {
								log.G(Ctx).Error(execReturn.Stderr)
								return nil, nil, errors.New(execReturn.Stderr)
							} else {
								log.G(Ctx).Debug("--- Created folder " + podSecretDir)
							}

							log.G(Ctx).Debug("--- Writing Secret files")
							for k, v := range secrets {
								// TODO: Ensure that these files are deleted in failure cases
								fullPath := filepath.Join(podSecretDir, k)
								os.WriteFile(fullPath, v, mode)
								if err != nil {
									log.G(Ctx).Errorf("Could not write Secret file %s", fullPath)
									err = os.RemoveAll(fullPath)
									if err != nil {
										log.G(Ctx).Error("Unable to remove file " + fullPath)
										return nil, nil, err
									}
									return nil, nil, err
								} else {
									log.G(Ctx).Debug("--- Written Secret file " + fullPath)
								}
							}
						}
						return secretNamePaths, envs, nil
					}

				case string:
					if podVolumeSpec != nil && podVolumeSpec.EmptyDir != nil {
						var edPath string
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
							return nil, nil, err
						} else {
							log.G(Ctx).Debug("-- Created EmptyDir in " + edPath)
						}

						edPath += (":" + mountSpec.MountPath + "/" + mountSpec.Name + ",")
						return []string{edPath}, nil, nil
					}
				}
			}
		}
	}
	return nil, nil, err
}
