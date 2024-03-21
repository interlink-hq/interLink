package slurm

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	exec2 "github.com/alexellis/go-execute/pkg/v1"
	"github.com/containerd/containerd/log"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	commonIL "github.com/intertwin-eu/interlink/pkg/common"
)

type SidecarHandler struct {
	Config commonIL.InterLinkConfig
	JIDs   *map[string]*JidStruct
	Ctx    context.Context
	Mutex  *sync.Mutex
}

var prefix string
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

// parsingTimeFromString parses time from a string and returns it into a variable of type time.Time.
// The format time can be specified in the 3rd argument.
func parsingTimeFromString(Ctx context.Context, stringTime string, timestampFormat string) (time.Time, error) {
	parsedTime := time.Time{}
	parts := strings.Fields(stringTime)
	if len(parts) != 4 {
		err := errors.New("invalid timestamp format")
		log.G(Ctx).Error(err)
		return time.Time{}, err
	}

	parsedTime, err := time.Parse(timestampFormat, stringTime)
	if err != nil {
		log.G(Ctx).Error(err)
		return time.Time{}, err
	}

	return parsedTime, nil
}

// CreateDirectories is just a function to be sure directories exists at runtime
func CreateDirectories(config commonIL.InterLinkConfig) error {
	path := config.DataRootFolder
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

// LoadJIDs loads Job IDs into the main JIDs struct from files in the root folder.
// It's useful went down and needed to be restarded, but there were jobs running, for example.
// Return only error in case of failure
func LoadJIDs(Ctx context.Context, config commonIL.InterLinkConfig, mutex *sync.Mutex, JIDs *map[string]*JidStruct) error {
	path := config.DataRootFolder

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

	mutex.Lock()
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
				StartedAt, err = parsingTimeFromString(Ctx, string(StartedAtString), "2006-01-02 15:04:05.999999999 -0700 MST")
				if err != nil {
					log.G(Ctx).Debug(err)
				}
			}

			FinishedAtString, err := os.ReadFile(path + entry.Name() + "/" + "FinishedAt.time")
			if err != nil {
				log.G(Ctx).Debug(err)
			} else {
				FinishedAt, err = parsingTimeFromString(Ctx, string(FinishedAtString), "2006-01-02 15:04:05.999999999 -0700 MST")
				if err != nil {
					log.G(Ctx).Debug(err)
				}
			}
			JIDEntry := JidStruct{PodUID: podUID, JID: string(JID), StartTime: StartedAt, EndTime: FinishedAt}
			(*JIDs)[podUID] = &JIDEntry
		}
	}
	mutex.Unlock()

	return nil
}

// prepareEnvs reads all Environment variables from a container and append them to a slice of strings.
// It returns the slice containing all envs in the form of key=value.
func prepareEnvs(Ctx context.Context, container v1.Container) []string {
	if len(container.Env) > 0 {
		log.G(Ctx).Info("-- Appending envs")
		env := make([]string, 1)
		env = append(env, "--env")
		env_data := ""
		for _, envVar := range container.Env {
			tmp := (envVar.Name + "=" + envVar.Value + ",")
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

// prepareMounts iterates along the struct provided in the data parameter and checks for ConfigMaps, Secrets and EmptyDirs to be mounted.
// For each element found, the mountData function is called.
// In this context, the general case is given by host and container not sharing the file system, so data are stored within ENVS with matching names.
// The content of these ENVS will be written to a text file by the generated SLURM script later, so the container will be able to mount these files.
// The command to write files is appended in the global "prefix" variable.
// It returns a string composed as the singularity --bind command to bind mount directories and files and the first encountered error.
func prepareMounts(
	Ctx context.Context,
	config commonIL.InterLinkConfig,
	data []commonIL.RetrievedPodData,
	container v1.Container,
	workingPath string,
) ([]string, error) {
	log.G(Ctx).Info("-- Preparing mountpoints for " + container.Name)
	mount := make([]string, 1)
	mount = append(mount, "--bind")
	mountedData := ""

	for _, podData := range data {
		err := os.MkdirAll(workingPath, os.ModePerm)
		if err != nil {
			log.G(Ctx).Error(err)
			return nil, err
		} else {
			log.G(Ctx).Info("-- Created directory " + workingPath)
		}

		for _, cont := range podData.Containers {
			for _, cfgMap := range cont.ConfigMaps {
				if container.Name == cont.Name {
					configMapsPaths, envs, err := mountData(Ctx, config, podData.Pod, container, cfgMap, workingPath)
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
						}
						mountedData += path
					}
				}
			}

			for _, secret := range cont.Secrets {
				if container.Name == cont.Name {
					secretsPaths, envs, err := mountData(Ctx, config, podData.Pod, container, secret, workingPath)
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
						}
						mountedData += path
					}
				}
			}

			for _, emptyDir := range cont.EmptyDirs {
				if container.Name == cont.Name {
					paths, _, err := mountData(Ctx, config, podData.Pod, container, emptyDir, workingPath)
					if err != nil {
						log.G(Ctx).Error(err)
						return nil, err
					}
					for _, path := range paths {
						mountedData += path
					}
				}
			}
		}
	}

	if last := len(mountedData) - 1; last >= 0 && mountedData[last] == ',' {
		mountedData = mountedData[:last]
	}
	if len(mountedData) == 0 {
		return []string{}, nil
	}
	return append(mount, mountedData), nil
}

// produceSLURMScript generates a SLURM script according to data collected.
// It must be called after ENVS and mounts are already set up since
// it relies on "prefix" variable being populated with needed data and ENVS passed in the commands parameter.
// It returns the path to the generated script and the first encountered error.
func produceSLURMScript(
	Ctx context.Context,
	config commonIL.InterLinkConfig,
	podNamespace string,
	podUID string,
	path string,
	metadata metav1.ObjectMeta,
	commands []SingularityCommand,
) (string, error) {
	log.G(Ctx).Info("-- Creating file for the Slurm script")
	err := os.MkdirAll(path, os.ModePerm)
	if err != nil {
		log.G(Ctx).Error(err)
		return "", err
	} else {
		log.G(Ctx).Info("-- Created directory " + path)
	}
	postfix := ""

	f, err := os.Create(path + "/job.sh")
	if err != nil {
		log.G(Ctx).Error(err)
		return "", err
	}
	err = os.Chmod(path+"/job.sh", 0774)
	if err != nil {
		log.G(Ctx).Error(err)
		return "", err
	}
	defer f.Close()

	if err != nil {
		log.G(Ctx).Error("Unable to create file " + path + "/job.sh")
		return "", err
	} else {
		log.G(Ctx).Debug("--- Created file " + path + "/job.sh")
	}

	var sbatchFlagsFromArgo []string
	var sbatchFlagsAsString = ""
	if slurmFlags, ok := metadata.Annotations["slurm-job.vk.io/flags"]; ok {
		sbatchFlagsFromArgo = strings.Split(slurmFlags, " ")
	}
	if mpiFlags, ok := metadata.Annotations["slurm-job.vk.io/mpi-flags"]; ok {
		if mpiFlags != "true" {
			mpi := append([]string{"mpiexec", "-np", "$SLURM_NTASKS"}, strings.Split(mpiFlags, " ")...)
			for _, singularityCommand := range commands {
				singularityCommand.command = append(mpi, singularityCommand.command...)
			}
		}
	}

	for _, slurmFlag := range sbatchFlagsFromArgo {
		sbatchFlagsAsString += "\n#SBATCH " + slurmFlag
	}

	if config.Tsocks {
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

		prefix += "\nssh -4 -N -D $port " + config.Tsockslogin + " &"
		prefix += "\nSSH_PID=$!"
		prefix += "\necho \"local = 10.0.0.0/255.0.0.0 \nserver = 127.0.0.1 \nserver_port = $port\" >> .tmp/" + podUID + "_tsocks.conf"
		prefix += "\nexport TSOCKS_CONF_FILE=.tmp/" + podUID + "_tsocks.conf && export LD_PRELOAD=" + config.Tsockspath
	}

	if config.Commandprefix != "" {
		prefix += "\n" + config.Commandprefix
	}

	if preExecAnnotations, ok := metadata.Annotations["job.vk.io/pre-exec"]; ok {
		prefix += "\n" + preExecAnnotations
	}

	sbatch_macros := "#!" + config.BashPath +
		"\n#SBATCH --job-name=" + podUID +
		"\n#SBATCH --output=" + path + "/job.out" +
		sbatchFlagsAsString +
		"\n" +
		prefix +
		"\n"

	log.G(Ctx).Debug("--- Writing file")

	var stringToBeWritten string

	stringToBeWritten += sbatch_macros

	for _, singularityCommand := range commands {
		stringToBeWritten += "\n" + strings.Join(singularityCommand.command[:], " ") +
			" &> " + path + "/" + singularityCommand.containerName + ".out; " +
			"echo $? > " + path + "/" + singularityCommand.containerName + ".status &"
	}

	stringToBeWritten += "\n" + postfix

	_, err = f.WriteString(stringToBeWritten)

	if err != nil {
		log.G(Ctx).Error(err)
		return "", err
	} else {
		log.G(Ctx).Debug("---- Written file")
	}

	return f.Name(), nil
}

// SLURMBatchSubmit submits the job provided in the path argument to the SLURM queue.
// At this point, it's up to the SLURM scheduler to manage the job.
// Returns the output of the sbatch command and the first encoundered error.
func SLURMBatchSubmit(Ctx context.Context, config commonIL.InterLinkConfig, path string) (string, error) {
	log.G(Ctx).Info("- Submitting Slurm job")
	shell := exec2.ExecTask{
		Command: "sh",
		Args:    []string{"-c", "\"" + config.Sbatchpath + " " + path + "\""},
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

// handleJID creates a JID file to store the Job ID of the submitted job.
// The output parameter must be the output of SLURMBatchSubmit function and the path
// is the path where to store the JID file.
// It also adds the JID to the JIDs main structure.
// Return the first encountered error.
func handleJID(Ctx context.Context, mutex *sync.Mutex, pod v1.Pod, podUID string, JIDs *map[string]*JidStruct, output string, path string) error {
	r := regexp.MustCompile(`Submitted batch job (?P<jid>\d+)`)
	jid := r.FindStringSubmatch(output)
	f, err := os.Create(path + "/JobID.jid")
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
	mutex.Lock()
	(*JIDs)[podUID] = &JidStruct{PodUID: string(pod.UID), JID: jid[1]}
	mutex.Unlock()
	log.G(Ctx).Info("Job ID is: " + (*JIDs)[podUID].JID)
	return nil
}

// removeJID delete a JID from the structure
func removeJID(mutex *sync.Mutex, podUID string, JIDs *map[string]*JidStruct) {
	mutex.Lock()
	delete(*JIDs, podUID)
	mutex.Unlock()
}

// deleteContainer checks if a Job has not yet been deleted and, in case, calls the scancel command to abort the job execution.
// It then removes the JID from the main JIDs structure and all the related files on the disk.
// Returns the first encountered error.
func deleteContainer(Ctx context.Context, config commonIL.InterLinkConfig, mutex *sync.Mutex, podUID string, JIDs *map[string]*JidStruct, path string) error {
	log.G(Ctx).Info("- Deleting Job for pod " + podUID)
	if checkIfJidExists(JIDs, podUID) {
		_, err := exec.Command(config.Scancelpath, (*JIDs)[podUID].JID).Output()
		if err != nil {
			log.G(Ctx).Error(err)
			return err
		} else {
			log.G(Ctx).Info("- Deleted Job ", (*JIDs)[podUID].JID)
		}
	}
	err := os.RemoveAll(path + "/" + podUID)
	removeJID(mutex, podUID, JIDs)
	if err != nil {
		log.G(Ctx).Error(err)
		return err
	}
	return err
}

// mountData is called by prepareMounts and creates files and directory according to their definition in the pod structure.
// The data parameter is an interface and it can be of type v1.ConfigMap, v1.Secret and string (for the empty dir).
// Returns 2 slices of string, one containing the ConfigMaps/Secrets/EmptyDirs paths and one the list of relatives ENVS to be used
// to create the files inside the container.
// It also returns the first encountered error.
func mountData(Ctx context.Context, config commonIL.InterLinkConfig, pod v1.Pod, container v1.Container, data interface{}, path string) ([]string, []string, error) {
	if config.ExportPodData {
		for _, mountSpec := range container.VolumeMounts {
			var podVolumeSpec *v1.VolumeSource

			for _, vol := range pod.Spec.Volumes {
				if vol.Name == mountSpec.Name {
					podVolumeSpec = &vol.VolumeSource

					switch mount := data.(type) {
					case v1.ConfigMap:
						configMaps := make(map[string]string)
						var configMapNamePaths []string
						var envs []string

						err := os.RemoveAll(path + "/configMaps/" + vol.Name)

						if err != nil {
							log.G(Ctx).Error("Unable to delete root folder")
							return nil, nil, err
						}

						if podVolumeSpec != nil && podVolumeSpec.ConfigMap != nil {
							log.G(Ctx).Info("--- Mounting ConfigMap " + podVolumeSpec.ConfigMap.Name)
							mode := os.FileMode(*podVolumeSpec.ConfigMap.DefaultMode)
							podConfigMapDir := filepath.Join(path+"/", "configMaps/", vol.Name)

							if mount.Data != nil {
								for key := range mount.Data {
									configMaps[key] = mount.Data[key]
									fullPath := filepath.Join(podConfigMapDir, key)
									fullPath += (":" + mountSpec.MountPath + "/" + key + ",")
									configMapNamePaths = append(configMapNamePaths, fullPath)

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

						err := os.RemoveAll(path + "/secrets/" + vol.Name)

						if err != nil {
							log.G(Ctx).Error("Unable to delete root folder")
							return nil, nil, err
						}

						if podVolumeSpec != nil && podVolumeSpec.Secret != nil {
							log.G(Ctx).Info("--- Mounting Secret " + podVolumeSpec.Secret.SecretName)
							mode := os.FileMode(*podVolumeSpec.Secret.DefaultMode)
							podSecretDir := filepath.Join(path+"/", "secrets/", vol.Name)

							if mount.Data != nil {
								for key := range mount.Data {
									secrets[key] = mount.Data[key]
									fullPath := filepath.Join(podSecretDir, key)
									fullPath += (":" + mountSpec.MountPath + "/" + key + ",")
									secretNamePaths = append(secretNamePaths, fullPath)

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
							edPath = filepath.Join(path + "/" + "emptyDirs/" + vol.Name)
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
	}
	return nil, nil, nil
}

// checkIfJidExists checks if a JID is in the main JIDs struct
func checkIfJidExists(JIDs *map[string]*JidStruct, uid string) bool {
	_, ok := (*JIDs)[uid]

	if ok {
		return true
	} else {
		return false
	}
}
