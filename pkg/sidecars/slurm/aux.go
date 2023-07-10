package slurm

import (
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	exec2 "github.com/alexellis/go-execute/pkg/v1"
	commonIL "github.com/cloud-pg/interlink/pkg/common"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type JidStruct struct {
	JID string
	Pod v1.Pod
}

func prepare_envs(container v1.Container) []string {
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
	env = append(env, env_data)

	return env
}

func prepare_mounts(container v1.Container, pod *v1.Pod) []string {
	mount := make([]string, 1)
	mount = append(mount, "--bind")
	mount_data := ""
	pod_name := strings.Split(container.Name, "-")

	if len(pod_name) > 6 {
		pod_name = pod_name[0:6]
	}

	err := os.MkdirAll(".knoc/"+strings.Join(pod_name[:len(pod_name)-1], "-"), os.ModePerm)
	if err != nil {
		log.Fatalln("Cant create directory")
	}

	for _, mount_var := range container.VolumeMounts {

		var podVolumeSpec *v1.VolumeSource
		path := ""
		log.Print(path)

		for _, vol := range pod.Spec.Volumes {

			if vol.Name == mount_var.Name {
				podVolumeSpec = &vol.VolumeSource
			}

			if podVolumeSpec != nil && podVolumeSpec.ConfigMap != nil {

				configMapsPaths := mountConfigMaps(container, pod)
				fmt.Println(configMapsPaths)
				for _, path := range configMapsPaths {
					mount_data += path
				}

			} else if podVolumeSpec != nil && podVolumeSpec.Secret != nil {
				secretsPaths := mountSecrets(container, pod)
				fmt.Println(secretsPaths)
				for _, path := range secretsPaths {
					mount_data += path
				}
			} else if podVolumeSpec != nil && podVolumeSpec.EmptyDir != nil {
				path := mountEmptyDir(container, pod)
				mount_data += path

			} else {
				/* path = filepath.Join(commonIL.InterLinkConfigInst.DataRootFolder, pod.Namespace+"-"+string(pod.UID)+"/", mount_var.Name)
				path = (".knoc/" + strings.Join(pod_name, "-") + "/" + mount_var.Name + ":" + mount_var.MountPath + ",")
				mount_data += path */
				log.Println("To be implemented")
			}
		}
	}
	path_hardcoded := ("/cvmfs/grid.cern.ch/etc/grid-security:/etc/grid-security" + "," +
		"/cvmfs:/cvmfs" + "," +
		"/exa5/scratch/user/spigad" + "," +
		"/exa5/scratch/user/spigad/CMS/SITECONF" + ",")
	mount_data += path_hardcoded
	if last := len(mount_data) - 1; last >= 0 && mount_data[last] == ',' {
		mount_data = mount_data[:last]
	}
	return append(mount, mount_data)
}

func produce_slurm_script(container v1.Container, metadata metav1.ObjectMeta, command []string) string {
	newpath := filepath.Join(".", ".tmp")
	err := os.MkdirAll(newpath, os.ModePerm)
	f, err := os.Create(".tmp/" + container.Name + ".sh")
	if err != nil {
		log.Fatalln("Cant create slurm_script")
	}
	var sbatch_flags_from_argo []string
	var sbatch_flags_as_string = ""
	if slurm_flags, ok := metadata.Annotations["slurm-job.knoc.io/flags"]; ok {
		sbatch_flags_from_argo = strings.Split(slurm_flags, " ")
		log.Print(sbatch_flags_from_argo)
	}
	if mpi_flags, ok := metadata.Annotations["slurm-job.knoc.io/mpi-flags"]; ok {
		if mpi_flags != "true" {
			mpi := append([]string{"mpiexec", "-np", "$SLURM_NTASKS"}, strings.Split(mpi_flags, " ")...)
			command = append(mpi, command...)
		}
		log.Print(mpi_flags)
	}
	for _, slurm_flag := range sbatch_flags_from_argo {
		sbatch_flags_as_string += "\n#SBATCH " + slurm_flag
	}

	prefix := ""
	postfix := ""

	if commonIL.InterLinkConfigInst.Tsocks {
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
		prefix += "\necho \"local = 10.0.0.0/255.0.0.0 \nserver = 127.0.0.1 \nserver_port = $port\" >> .tmp/" + container.Name + "_tsocks.conf"
		prefix += "\nexport TSOCKS_CONF_FILE=.tmp/" + container.Name + "_tsocks.conf && export LD_PRELOAD=" + commonIL.InterLinkConfigInst.Tsockspath
	}

	if commonIL.InterLinkConfigInst.Commandprefix != "" {
		prefix += "\n" + commonIL.InterLinkConfigInst.Commandprefix
	}

	sbatch_macros := "#!/bin/bash" +
		"\n#SBATCH --job-name=" + container.Name +
		sbatch_flags_as_string +
		"\n. ~/.bash_profile" +
		//"\nmodule load singularity" +
		"\nexport SINGULARITYENV_SINGULARITY_TMPDIR=$CINECA_SCRATCH" +
		"\nexport SINGULARITYENV_SINGULARITY_CACHEDIR=$CINECA_SCRATCH" +
		"\npwd; hostname; date" +
		prefix +
		"\n"
	f.WriteString(sbatch_macros + "\n" + strings.Join(command[:], " ") + " >> " + ".knoc/" + container.Name + ".out 2>> " + ".knoc/" + container.Name + ".err \n echo $? > " + ".knoc/" + container.Name + ".status" + postfix)
	f.Close()
	return ".tmp/" + container.Name + ".sh"
}

func slurm_batch_submit(path string) string {
	cmd := []string{path}
	shell := exec2.ExecTask{
		Command: commonIL.InterLinkConfigInst.Sbatchpath,
		Args:    cmd,
		Shell:   true,
	}

	execReturn, _ := shell.Execute()
	execReturn.Stdout = strings.ReplaceAll(execReturn.Stdout, "\n", "")

	if execReturn.Stderr != "" {
		log.Println("Could not run sbatch. " + execReturn.Stderr)
	}
	return string(execReturn.Stdout)
}

func handle_jid(container v1.Container, output string, pod v1.Pod) {
	r := regexp.MustCompile(`Submitted batch job (?P<jid>\d+)`)
	jid := r.FindStringSubmatch(output)
	f, err := os.Create(".knoc/" + container.Name + ".jid")
	if err != nil {
		log.Println("Cant create jid_file")
	}
	f.WriteString(jid[1])
	JID = append(JID, JidStruct{JID: jid[1], Pod: pod})
	f.Close()
}

func delete_container(container v1.Container) {
	data, err := os.ReadFile(".knoc/" + container.Name + ".jid")
	if err != nil {
		log.Fatalln("Can't find job id of container")
	}
	jid, err := strconv.Atoi(string(data))
	if err != nil {
		log.Fatalln("Can't find job id of container")
	}
	_, err = exec.Command(commonIL.InterLinkConfigInst.Scancelpath, fmt.Sprint(jid)).Output()
	if err != nil {
		log.Println("Could not delete job", jid)
	} else {
		log.Println("Successfully deleted job ", jid)
	}
	exec.Command("rm", "-f ", ".knoc/"+container.Name+".out")
	exec.Command("rm", "-f ", ".knoc/"+container.Name+".err")
	exec.Command("rm", "-f ", ".knoc/"+container.Name+".status")
	exec.Command("rm", "-f ", ".knoc/"+container.Name+".jid")
	exec.Command("rm", "-rf", " .knoc/"+container.Name)
}

func mountConfigMaps(container v1.Container, pod *v1.Pod) []string { //returns an array containing mount paths for configMaps

	configMaps := make(map[string]string)
	var configMapNamePaths []string

	if commonIL.InterLinkConfigInst.ExportPodData {
		cmd := []string{"-rf " + commonIL.InterLinkConfigInst.DataRootFolder + "/configMaps"}
		shell := exec2.ExecTask{
			Command: "rm",
			Args:    cmd,
			Shell:   true,
		}

		_, err := shell.Execute()

		if err != nil {
			log.Println("Unable to delete root folder")
		}

		for _, mountSpec := range container.VolumeMounts {
			var podVolumeSpec *v1.VolumeSource

			for _, vol := range pod.Spec.Volumes {
				if vol.Name == mountSpec.Name {
					podVolumeSpec = &vol.VolumeSource
				}
				if podVolumeSpec != nil && podVolumeSpec.ConfigMap != nil {
					cmvs := podVolumeSpec.ConfigMap
					mode := os.FileMode(*podVolumeSpec.ConfigMap.DefaultMode)
					podConfigMapDir := filepath.Join(commonIL.InterLinkConfigInst.DataRootFolder, pod.Namespace+"-"+string(pod.UID)+"/", "configMaps/", vol.Name)

					cmd := []string{"get configmap " + cmvs.Name + " -o template --template='{{.data}}' -n " + pod.Namespace}
					shell := exec2.ExecTask{
						Command: "kubectl",
						Args:    cmd,
						Shell:   true,
					}

					execReturn, _ := shell.Execute()
					fmt.Println(execReturn)
					execReturn.Stdout = strings.ReplaceAll(execReturn.Stdout, "map[", "")
					execReturn.Stdout = strings.ReplaceAll(execReturn.Stdout, "]", "")
					returnedConfigMapsArray := make([]string, 0)

					if strings.Compare(execReturn.Stdout, "") != 0 {
						returnedConfigMapsArray = strings.Split(execReturn.Stdout, " ")
					}

					if returnedConfigMapsArray != nil {
						for _, element := range returnedConfigMapsArray {
							parts := strings.Split(element, ":")
							key := parts[0]
							value := parts[1]
							configMaps[key] = value
							path := filepath.Join(podConfigMapDir, key)
							path += (":" + mountSpec.MountPath + "/" + key + ",")
							configMapNamePaths = append(configMapNamePaths, path)
						}
					}

					if configMaps == nil {
						continue
					}

					cmd = []string{"-p " + podConfigMapDir}
					shell = exec2.ExecTask{
						Command: "mkdir",
						Args:    cmd,
						Shell:   true,
					}

					execReturn, err := shell.Execute()
					if err != nil {
						log.Panicln(err)
					}

					log.Printf("%v", "create dir for configmaps "+podConfigMapDir)

					for k, v := range configMaps {
						// TODO: Ensure that these files are deleted in failure cases
						fullPath := filepath.Join(podConfigMapDir, k)
						os.WriteFile(fullPath, []byte(v), mode)
						if err != nil {
							fmt.Printf("Could not write configmap file %s", fullPath)
						}
					}
				}
			}
		}
	}
	return configMapNamePaths
}

func mountSecrets(container v1.Container, pod *v1.Pod) []string { //returns an array containing mount paths for secrets
	secrets := make(map[string][]byte)
	var secretNamePaths []string

	if commonIL.InterLinkConfigInst.ExportPodData {
		cmd := []string{"-rf " + commonIL.InterLinkConfigInst.DataRootFolder + "/secrets"}
		shell := exec2.ExecTask{
			Command: "rm",
			Args:    cmd,
			Shell:   true,
		}

		_, err := shell.Execute()

		if err != nil {
			log.Println("Unable to delete root folder")
		}

		for _, mountSpec := range container.VolumeMounts {
			var podVolumeSpec *v1.VolumeSource

			for _, vol := range pod.Spec.Volumes {
				if vol.Name == mountSpec.Name {
					podVolumeSpec = &vol.VolumeSource
				}
				if podVolumeSpec != nil && podVolumeSpec.Secret != nil {
					svs := podVolumeSpec.Secret
					mode := os.FileMode(*podVolumeSpec.Secret.DefaultMode)
					fmt.Println(mode)
					podSecretDir := filepath.Join(commonIL.InterLinkConfigInst.DataRootFolder, pod.Namespace+"-"+string(pod.UID)+"/", "secrets/", vol.Name)

					cmd := []string{"get secret " + svs.SecretName + " -o jsonpath='{.data}' -n " + pod.Namespace}
					shell := exec2.ExecTask{
						Command: "kubectl",
						Args:    cmd,
						Shell:   true,
					}

					execReturn, _ := shell.Execute()
					execReturn.Stdout = strings.ReplaceAll(execReturn.Stdout, "\"", "")
					execReturn.Stdout = strings.ReplaceAll(execReturn.Stdout, "{", "")
					execReturn.Stdout = strings.ReplaceAll(execReturn.Stdout, "}", "")
					returnedSecretsArray := make([]string, 0)

					if strings.Compare(execReturn.Stdout, "") != 0 {
						returnedSecretsArray = strings.Split(execReturn.Stdout, " ")
					}

					if returnedSecretsArray != nil {
						for _, element := range returnedSecretsArray {
							parts := strings.Split(element, ":")
							key := parts[0]
							value, _ := base64.StdEncoding.DecodeString(parts[1])
							secrets[key] = value
							path := filepath.Join(podSecretDir, key)
							path += (":" + mountSpec.MountPath + "/" + key + ",")
							secretNamePaths = append(secretNamePaths, path)
						}
					}

					if secrets == nil {
						continue
					}

					cmd = []string{"-p " + podSecretDir}
					shell = exec2.ExecTask{
						Command: "mkdir",
						Args:    cmd,
						Shell:   true,
					}

					execReturn, err := shell.Execute()
					if err != nil {
						log.Print(err)
					}
					log.Printf("%v", "create dir for secrets "+podSecretDir)

					for k, v := range secrets {
						// TODO: Ensure that these files are deleted in failure cases
						fullPath := filepath.Join(podSecretDir, k)
						os.WriteFile(fullPath, v, mode)
						if err != nil {
							log.Printf("Could not write secrets file %s", fullPath)
						}
					}
				}
			}
		}
	}
	return secretNamePaths
}

func mountEmptyDir(container v1.Container, pod *v1.Pod) string {
	var edPath string

	if commonIL.InterLinkConfigInst.ExportPodData {
		cmd := []string{"-rf " + commonIL.InterLinkConfigInst.DataRootFolder + "/emptyDirs"}
		shell := exec2.ExecTask{
			Command: "rm",
			Args:    cmd,
			Shell:   true,
		}

		_, err := shell.Execute()

		if err != nil {
			log.Println("Unable to delete root folder")
		}

		for _, mountSpec := range container.VolumeMounts {
			var podVolumeSpec *v1.VolumeSource

			for _, vol := range pod.Spec.Volumes {
				if vol.Name == mountSpec.Name {
					podVolumeSpec = &vol.VolumeSource
				}
				if podVolumeSpec != nil && podVolumeSpec.EmptyDir != nil {
					// pod-global directory
					edPath = filepath.Join(commonIL.InterLinkConfigInst.DataRootFolder, pod.Namespace+"-"+string(pod.UID)+"/"+"emptyDirs/"+vol.Name)
					// mounted for every container
					cmd := []string{"-p " + edPath}
					shell := exec2.ExecTask{
						Command: "mkdir",
						Args:    cmd,
						Shell:   true,
					}

					_, err := shell.Execute()
					if err != nil {
						log.Print(err)
					}

					edPath += (":" + mountSpec.MountPath + "/" + mountSpec.Name + ",")
				}
			}
		}
	}
	return edPath
}
