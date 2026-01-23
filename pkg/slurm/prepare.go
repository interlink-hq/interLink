//nolint:revive,gocritic,gocyclo,ineffassign,unconvert,goconst,staticcheck
package slurm

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"al.essio.dev/pkg/shellescape"
	exec2 "github.com/alexellis/go-execute/pkg/v1"
	"github.com/containerd/containerd/log"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	commonIL "github.com/interlink-hq/interlink/pkg/interlink"

	"go.opentelemetry.io/otel/attribute"
	trace "go.opentelemetry.io/otel/trace"
)

type SidecarHandler struct {
	Config SlurmConfig
	JIDs   *map[string]*JidStruct
	ctx    context.Context
}

var (
	prefix string
)

type JidStruct struct {
	PodUID       string    `json:"PodUID"`
	PodNamespace string    `json:"PodNamespace"`
	JID          string    `json:"JID"`
	StartTime    time.Time `json:"StartTime"`
	EndTime      time.Time `json:"EndTime"`
}

type ResourceLimits struct {
	CPU    int64
	Memory int64
}

// FlavorResolution holds the resolved flavor information
type FlavorResolution struct {
	FlavorName    string
	CPUDefault    int64
	MemoryDefault int64  // in bytes
	UID           *int64 // Optional User ID for this flavor
	SlurmFlags    []string
}

func extractHeredoc(content, marker string) (string, error) {
	// Find the start of the heredoc
	startPattern := fmt.Sprintf("cat <<'%s'", marker)
	startIdx := strings.Index(content, startPattern)
	if startIdx == -1 {
		return "", fmt.Errorf("heredoc start marker not found")
	}

	// Find the line after the cat command (start of actual content)
	contentStart := strings.Index(content[startIdx:], "\n")
	if contentStart == -1 {
		return "", fmt.Errorf("invalid heredoc format")
	}
	contentStart += startIdx + 1

	// Find the end marker
	endMarker := "\n" + marker
	endIdx := strings.Index(content[contentStart:], endMarker)
	if endIdx == -1 {
		return "", fmt.Errorf("heredoc end marker not found")
	}

	// Extract the content between start and end markers
	return content[contentStart : contentStart+endIdx], nil
}

func removeHeredoc(content, marker string) string {
	// Find the start of the heredoc
	startPattern := fmt.Sprintf("cat <<'%s'", marker)
	startIdx := strings.Index(content, startPattern)
	if startIdx == -1 {
		return content // No heredoc found, return as-is
	}

	// Find the line after the cat command (start of actual content)
	contentStart := strings.Index(content[startIdx:], "\n")
	if contentStart == -1 {
		return content // Invalid heredoc format
	}
	contentStart += startIdx + 1

	// Find the end marker
	endMarker := "\n" + marker
	endIdx := strings.Index(content[contentStart:], endMarker)
	if endIdx == -1 {
		return content // Heredoc end marker not found
	}

	// Calculate the actual end position (after the end marker)
	heredocEnd := contentStart + endIdx + len(endMarker)

	// Skip trailing newline if present
	if heredocEnd < len(content) && content[heredocEnd] == '\n' {
		heredocEnd++
	}

	// Remove the heredoc block and return
	return content[:startIdx] + content[heredocEnd:]
}

// stringToHex encodes the provided str string into a hex string and removes all trailing redundant zeroes to keep the output more compact
func stringToHex(str string) string {
	var buffer bytes.Buffer
	for _, char := range str {
		err := binary.Write(&buffer, binary.LittleEndian, char)
		if err != nil {
			fmt.Println("Error converting character:", err)
			return ""
		}
	}

	hexString := hex.EncodeToString(buffer.Bytes())
	hexBytes := []byte(hexString)
	var hexReturn string
	for i := 0; i < len(hexBytes); i += 2 {
		if hexBytes[i] != 48 && hexBytes[i+1] != 48 {
			hexReturn += string(hexBytes[i]) + string(hexBytes[i+1])
		}
	}
	return hexReturn
}

// parsingTimeFromString parses time from a string and returns it into a variable of type time.Time.
// The format time can be specified in the 3rd argument.
func parsingTimeFromString(ctx context.Context, stringTime string, timestampFormat string) (time.Time, error) {
	parts := strings.Fields(stringTime)
	if len(parts) != 4 {
		err := errors.New("invalid timestamp format")
		log.G(ctx).Error(err)
		return time.Time{}, err
	}

	parsedTime, err := time.Parse(timestampFormat, stringTime)
	if err != nil {
		log.G(ctx).Error(err)
		return time.Time{}, err
	}

	return parsedTime, nil
}

// parseMemoryString converts memory string formats (e.g., "16G", "32000M", "1024") to bytes
func parseMemoryString(memStr string) (int64, error) {
	if memStr == "" {
		return 0, nil
	}

	memStr = strings.TrimSpace(strings.ToUpper(memStr))

	// Check for suffix
	if strings.HasSuffix(memStr, "G") || strings.HasSuffix(memStr, "GB") {
		numStr := strings.TrimSuffix(strings.TrimSuffix(memStr, "B"), "G")
		val, err := strconv.ParseInt(numStr, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid memory format %s: %w", memStr, err)
		}
		return val * 1024 * 1024 * 1024, nil
	} else if strings.HasSuffix(memStr, "M") || strings.HasSuffix(memStr, "MB") {
		numStr := strings.TrimSuffix(strings.TrimSuffix(memStr, "B"), "M")
		val, err := strconv.ParseInt(numStr, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid memory format %s: %w", memStr, err)
		}
		return val * 1024 * 1024, nil
	} else if strings.HasSuffix(memStr, "K") || strings.HasSuffix(memStr, "KB") {
		numStr := strings.TrimSuffix(strings.TrimSuffix(memStr, "B"), "K")
		val, err := strconv.ParseInt(numStr, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid memory format %s: %w", memStr, err)
		}
		return val * 1024, nil
	}

	// No suffix, assume bytes
	val, err := strconv.ParseInt(memStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid memory format %s: %w", memStr, err)
	}
	return val, nil
}

// detectGPUResources checks if the pod requests GPU resources and returns the GPU count
func detectGPUResources(ctx context.Context, containers []v1.Container) int64 {
	var totalGPUs int64 = 0

	for _, container := range containers {
		// Check for nvidia.com/gpu
		if gpuLimit, ok := container.Resources.Limits["nvidia.com/gpu"]; ok {
			gpuCount := gpuLimit.Value()
			if gpuCount > 0 {
				log.G(ctx).Infof("Detected %d NVIDIA GPU(s) requested in container %s", gpuCount, container.Name)
				totalGPUs += gpuCount
			}
		}

		// Check for amd.com/gpu
		if gpuLimit, ok := container.Resources.Limits["amd.com/gpu"]; ok {
			gpuCount := gpuLimit.Value()
			if gpuCount > 0 {
				log.G(ctx).Infof("Detected %d AMD GPU(s) requested in container %s", gpuCount, container.Name)
				totalGPUs += gpuCount
			}
		}
	}

	return totalGPUs
}

// extractGPUCountFromFlags extracts GPU count from SLURM flags like --gres=gpu:2
func extractGPUCountFromFlags(flags []string) int64 {
	gresPattern := regexp.MustCompile(`--gres=gpu:(\d+)`)
	for _, flag := range flags {
		matches := gresPattern.FindStringSubmatch(flag)
		if len(matches) > 1 {
			if count, err := strconv.ParseInt(matches[1], 10, 64); err == nil {
				return count
			}
		}
	}
	return 0
}

// hasGPUInFlags checks if any SLURM flag contains GPU-related configuration
func hasGPUInFlags(flags []string) bool {
	for _, flag := range flags {
		if strings.Contains(flag, "--gres=gpu") || strings.Contains(flag, "gpu") {
			return true
		}
	}
	return false
}

// deduplicateSlurmFlags removes duplicate SLURM flags, keeping the last occurrence
// This implements proper priority: later flags override earlier ones
func deduplicateSlurmFlags(flags []string) []string {
	// Map to track flag keys and their last values
	flagMap := make(map[string]string)
	var order []string // Track order of first appearance

	for _, flag := range flags {
		flag = strings.TrimSpace(flag)
		if flag == "" {
			continue
		}

		// Extract the flag key (e.g., "--partition" from "--partition=cpu")
		key := flag
		if strings.Contains(flag, "=") {
			parts := strings.SplitN(flag, "=", 2)
			key = parts[0]
		} else if strings.HasPrefix(flag, "--") {
			// Handle flags like "--flag value" (split on space)
			parts := strings.Fields(flag)
			if len(parts) > 0 {
				key = parts[0]
			}
		}

		// If we haven't seen this key before, track its order
		if _, exists := flagMap[key]; !exists {
			order = append(order, key)
		}

		// Update the value (later occurrences override earlier ones)
		flagMap[key] = flag
	}

	// Rebuild the slice in original order with deduplicated values
	result := make([]string, 0, len(order))
	for _, key := range order {
		result = append(result, flagMap[key])
	}

	return result
}

// resolveFlavor determines which flavor to use based on annotations, GPU detection, and default flavor
func resolveFlavor(ctx context.Context, config SlurmConfig, metadata metav1.ObjectMeta, containers []v1.Container) (*FlavorResolution, error) {
	// No flavors configured, return nil
	if len(config.Flavors) == 0 {
		return nil, nil
	}

	var selectedFlavor *FlavorConfig
	var flavorName string

	// Priority 1: Check for explicit flavor annotation
	if annotationFlavor, ok := metadata.Annotations["slurm-job.vk.io/flavor"]; ok {
		if flavor, exists := config.Flavors[annotationFlavor]; exists {
			flavorCopy := flavor
			selectedFlavor = &flavorCopy
			flavorName = annotationFlavor
			log.G(ctx).Infof("Using flavor '%s' from annotation", flavorName)
		} else {
			log.G(ctx).Warningf("Flavor '%s' specified in annotation not found, falling back to auto-detection", annotationFlavor)
		}
	}

	// Priority 2: Auto-detect GPU and select GPU flavor
	if selectedFlavor == nil {
		gpuCount := detectGPUResources(ctx, containers)
		if gpuCount > 0 {
			log.G(ctx).Infof("Detected %d GPU(s) requested, searching for matching flavor", gpuCount)

			// Find best matching GPU flavor
			// Priority: exact GPU count match > any GPU flavor > name contains "gpu"
			var exactMatchFlavor *FlavorConfig
			var exactMatchName string
			var anyGPUFlavor *FlavorConfig
			var anyGPUName string

			for name, flavor := range config.Flavors {
				if !hasGPUInFlags(flavor.SlurmFlags) && !strings.Contains(strings.ToLower(name), "gpu") {
					continue
				}

				flavorGPUCount := extractGPUCountFromFlags(flavor.SlurmFlags)
				if flavorGPUCount == gpuCount {
					// Exact match - prefer this
					flavorCopy := flavor
					exactMatchFlavor = &flavorCopy
					exactMatchName = name
					break
				} else if hasGPUInFlags(flavor.SlurmFlags) && anyGPUFlavor == nil {
					// Any GPU flavor - use as fallback
					flavorCopy := flavor
					anyGPUFlavor = &flavorCopy
					anyGPUName = name
				}
			}

			if exactMatchFlavor != nil {
				selectedFlavor = exactMatchFlavor
				flavorName = exactMatchName
				log.G(ctx).Infof("Auto-detected GPU resources, using exact match flavor '%s' with %d GPU(s)", flavorName, gpuCount)
			} else if anyGPUFlavor != nil {
				selectedFlavor = anyGPUFlavor
				flavorName = anyGPUName
				log.G(ctx).Infof("Auto-detected GPU resources, using GPU flavor '%s' (no exact GPU count match found)", flavorName)
			} else {
				log.G(ctx).Warningf("GPU resources detected but no GPU flavor found, falling back to default")
			}
		}
	}

	// Priority 3: Use default flavor
	if selectedFlavor == nil && config.DefaultFlavor != "" {
		if flavor, exists := config.Flavors[config.DefaultFlavor]; exists {
			flavorCopy := flavor
			selectedFlavor = &flavorCopy
			flavorName = config.DefaultFlavor
			log.G(ctx).Infof("Using default flavor '%s'", flavorName)
		}
	}

	// No flavor selected
	if selectedFlavor == nil {
		return nil, nil
	}

	// Parse memory default
	memoryBytes, err := parseMemoryString(selectedFlavor.MemoryDefault)
	if err != nil {
		return nil, fmt.Errorf("failed to parse memory for flavor %s: %w", flavorName, err)
	}

	return &FlavorResolution{
		FlavorName:    flavorName,
		CPUDefault:    selectedFlavor.CPUDefault,
		MemoryDefault: memoryBytes,
		UID:           selectedFlavor.UID,
		SlurmFlags:    selectedFlavor.SlurmFlags,
	}, nil
}

// CreateDirectories is just a function to be sure directories exists at runtime
func (h *SidecarHandler) CreateDirectories() error {
	path := h.Config.DataRootFolder
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
func (h *SidecarHandler) LoadJIDs() error {
	path := h.Config.DataRootFolder

	dir, err := os.Open(path)
	if err != nil {
		log.G(h.ctx).Error(err)
		return err
	}
	defer dir.Close()

	entries, err := dir.ReadDir(0)
	if err != nil {
		log.G(h.ctx).Error(err)
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			var podNamespace []byte
			var podUID []byte
			StartedAt := time.Time{}
			FinishedAt := time.Time{}

			JID, err := os.ReadFile(path + entry.Name() + "/" + "JobID.jid")
			if err != nil {
				log.G(h.ctx).Debug(err)
				continue
			} else {
				podUID, err = os.ReadFile(path + entry.Name() + "/" + "PodUID.uid")
				if err != nil {
					log.G(h.ctx).Debug(err)
					continue
				} else {
					podNamespace, err = os.ReadFile(path + entry.Name() + "/" + "PodNamespace.ns")
					if err != nil {
						log.G(h.ctx).Debug(err)
						continue
					}
				}

				StartedAtString, err := os.ReadFile(path + entry.Name() + "/" + "StartedAt.time")
				if err != nil {
					log.G(h.ctx).Debug(err)
				} else {
					StartedAt, err = parsingTimeFromString(h.ctx, string(StartedAtString), "2006-01-02 15:04:05.999999999 -0700 MST")
					if err != nil {
						log.G(h.ctx).Debug(err)
					}
				}
			}

			FinishedAtString, err := os.ReadFile(path + entry.Name() + "/" + "FinishedAt.time")
			if err != nil {
				log.G(h.ctx).Debug(err)
			} else {
				FinishedAt, err = parsingTimeFromString(h.ctx, string(FinishedAtString), "2006-01-02 15:04:05.999999999 -0700 MST")
				if err != nil {
					log.G(h.ctx).Debug(err)
				}
			}
			JIDEntry := JidStruct{PodUID: string(podUID), PodNamespace: string(podNamespace), JID: string(JID), StartTime: StartedAt, EndTime: FinishedAt}
			(*h.JIDs)[string(podUID)] = &JIDEntry
		}
	}

	return nil
}

func createEnvFile(ctx context.Context, config SlurmConfig, podData commonIL.RetrievedPodData, container v1.Container) ([]string, []string, error) {
	envs := []string{}
	// For debugging purpose only
	envsData := []string{}

	envfilePath := (config.DataRootFolder + podData.Pod.Namespace + "-" + string(podData.Pod.UID) + "/" + container.Name + "_envfile.properties")
	log.G(ctx).Info("-- Appending envs using envfile " + envfilePath)

	switch config.ContainerRuntime {
	case RuntimeSingularity:
		envs = append(envs, "--env-file")
		envs = append(envs, envfilePath)
	case RuntimeEnroot:
		mountEnvs := envfilePath + ":" + "/etc/environment"
		envs = append(envs, "--mount", mountEnvs)
	}

	envfile, err := os.Create(envfilePath)
	if err != nil {
		log.G(ctx).Error(err)
		return nil, nil, err
	}
	defer envfile.Close()

	for _, envVar := range container.Env {
		// The environment variable values can contains all sort of simple/double quote and space and any arbitrary values.
		// singularity reads the env-file and parse it like a shell string, so shellescape will escape any quote properly.
		tmpValue := shellescape.Quote(envVar.Value)
		tmp := (envVar.Name + "=" + tmpValue)

		envsData = append(envsData, tmp)

		_, err := envfile.WriteString(tmp + "\n")
		if err != nil {
			log.G(ctx).Error(err)
			return nil, nil, err
		} else {
			log.G(ctx).Debug("---- Written envfile file " + envfilePath + " key " + envVar.Name + " value " + tmpValue)
		}
	}

	// All env variables are written, we flush it now.
	err = envfile.Sync()
	if err != nil {
		log.G(ctx).Error(err)
		return nil, nil, err
	}

	// Calling Close() in case of error. If not error, the defer will close it again but it should be idempotent.
	envfile.Close()

	return envs, envsData, nil
}

// prepareEnvs reads all Environment variables from a container and append them to a envfile.properties. The values are sh-escaped.
// It returns the slice containing, if there are Environment variables, the arguments for envfile and its path, or else an empty array.
func prepareEnvs(ctx context.Context, config SlurmConfig, podData commonIL.RetrievedPodData, container v1.Container) []string {
	start := time.Now().UnixMicro()
	span := trace.SpanFromContext(ctx)
	span.AddEvent("Preparing ENVs for container " + container.Name)
	var envs []string = []string{}
	// For debugging purpose only
	envsData := []string{}
	var err error

	if len(container.Env) > 0 {
		envs, envsData, err = createEnvFile(ctx, config, podData, container)
		if err != nil {
			log.G(ctx).Error(err)
			return nil
		}
	}

	duration := time.Now().UnixMicro() - start
	span.AddEvent("Prepared ENVs for container "+container.Name, trace.WithAttributes(
		attribute.String("prepareenvs.container.name", container.Name),
		attribute.Int64("prepareenvs.duration", duration),
		attribute.StringSlice("prepareenvs.container.envs", envs),
		attribute.StringSlice("prepareenvs.container.envs_data", envsData)))

	return envs
}

func getRetrievedContainer(podData *commonIL.RetrievedPodData, containerName string) (*commonIL.RetrievedContainer, error) {
	for _, container := range podData.Containers {
		if container.Name == containerName {
			return &container, nil
		}
	}
	return nil, fmt.Errorf("could not find retrieved container for %s in pod %s", containerName, podData.Pod.Name)
}

func getRetrievedConfigMap(retrievedContainer *commonIL.RetrievedContainer, configMapName string, containerName string, podName string) (*v1.ConfigMap, error) {
	for _, configMap := range retrievedContainer.ConfigMaps {
		if configMap.Name == configMapName {
			return &configMap, nil
		}
	}
	return nil, fmt.Errorf("could not find configMap %s in container %s in pod %s", configMapName, containerName, podName)
}

func getRetrievedProjectedVolumeMap(retrievedContainer *commonIL.RetrievedContainer, projectedVolumeMapName string, containerName string, podName string) (*v1.ConfigMap, error) {
	for _, retrievedProjectedVolumeMap := range retrievedContainer.ProjectedVolumeMaps {
		if retrievedProjectedVolumeMap.Name == projectedVolumeMapName {
			return &retrievedProjectedVolumeMap, nil
		}
	}
	// This should not happen, either this is an error or the flag DisableProjectedVolumes is true in VK. Building context for log.
	return nil, nil
}

func getRetrievedSecret(retrievedContainer *commonIL.RetrievedContainer, secretName string, containerName string, podName string) (*v1.Secret, error) {
	for _, retrievedSecret := range retrievedContainer.Secrets {
		if retrievedSecret.Name == secretName {
			return &retrievedSecret, nil
		}
	}
	return nil, fmt.Errorf("could not find secret %s in container %s in pod %s", secretName, containerName, podName)
}

func getPodVolume(pod *v1.Pod, volumeName string) (*v1.Volume, error) {
	for _, vol := range pod.Spec.Volumes {
		if vol.Name == volumeName {
			return &vol, nil
		}
	}
	return nil, fmt.Errorf("could not find volume %s in pod %s", volumeName, pod.Name)
}

func prepareMountsSimpleVolume(
	ctx context.Context,
	config SlurmConfig,
	container *v1.Container,
	workingPath string,
	volumeObject interface{},
	volumeMount v1.VolumeMount,
	volume v1.Volume,
	mountedDataSB *strings.Builder,
) error {
	volumesHostToContainerPaths, envVarNames, err := mountData(ctx, config, container, volumeObject, volumeMount, volume, workingPath)
	if err != nil {
		log.G(ctx).Error(err)
		return err
	}

	log.G(ctx).Debug("volumesHostToContainerPaths: ", volumesHostToContainerPaths)

	for filePathIndex, volumesHostToContainerPath := range volumesHostToContainerPaths {
		if os.Getenv("SHARED_FS") != EnvSharedFSTrue {
			filePathSplitted := strings.Split(volumesHostToContainerPath, ":")
			hostFilePath := filePathSplitted[0]
			hostFilePathSplitted := strings.Split(hostFilePath, "/")
			hostParentDir := filepath.Join(hostFilePathSplitted[:len(hostFilePathSplitted)-1]...)

			// Creates parent dir of the file, then create empty file.
			prefix += "\nmkdir -p \"" + hostParentDir + "\" && touch " + hostFilePath

			// Puts content of the file thanks to env var. Note: the envVarNames has the same number and order that volumesHostToContainerPaths.
			envVarName := envVarNames[filePathIndex]
			splittedEnvName := strings.Split(envVarName, "_")
			log.G(ctx).Info(splittedEnvName[len(splittedEnvName)-1])
			prefix += "\necho \"${" + envVarName + "}\" > \"" + hostFilePath + "\""
		}
		switch config.ContainerRuntime {
		case RuntimeSingularity:
			mountedDataSB.WriteString(" --bind ")
		case RuntimeEnroot:
			mountedDataSB.WriteString(" --mount ")
		}
		mountedDataSB.WriteString(volumesHostToContainerPath)
	}
	return nil
}

// prepareMounts iterates along the struct provided in the data parameter and checks for ConfigMaps, Secrets and EmptyDirs to be mounted.
// For each element found, the mountData function is called.
// In this context, the general case is given by host and container not sharing the file system, so data are stored within ENVS with matching names.
// The content of these ENVS will be written to a text file by the generated SLURM script later, so the container will be able to mount these files.
// The command to write files is appended in the global "prefix" variable.
// It returns a string composed as the singularity --bind command to bind mount directories and files and the first encountered error.
//
//nolint:gocyclo
func prepareMounts(
	ctx context.Context,
	config SlurmConfig,
	podData *commonIL.RetrievedPodData,
	container *v1.Container,
	workingPath string,
) (string, error) {
	span := trace.SpanFromContext(ctx)
	start := time.Now().UnixMicro()
	log.G(ctx).Info(span)
	span.AddEvent("Preparing Mounts for container " + container.Name)

	log.G(ctx).Info("-- Preparing mountpoints for ", container.Name)
	var mountedDataSB strings.Builder

	err := os.MkdirAll(workingPath, os.ModePerm)
	if err != nil {
		log.G(ctx).Error(err)
		return "", err
	}
	log.G(ctx).Info("-- Created directory ", workingPath)
	podName := podData.Pod.Name

	for _, volumeMount := range container.VolumeMounts {
		volumePtr, err := getPodVolume(&podData.Pod, volumeMount.Name)
		volume := *volumePtr
		if err != nil {
			return "", err
		}

		retrievedContainer, err := getRetrievedContainer(podData, container.Name)
		if err != nil {
			return "", err
		}

		switch {
		case volume.ConfigMap != nil:
			retrievedConfigMap, err := getRetrievedConfigMap(retrievedContainer, volume.ConfigMap.Name, container.Name, podName)
			if err != nil {
				return "", err
			}

			err = prepareMountsSimpleVolume(ctx, config, container, workingPath, *retrievedConfigMap, volumeMount, volume, &mountedDataSB)
			if err != nil {
				return "", err
			}

		case volume.Projected != nil:
			retrievedProjectedVolumeMap, err := getRetrievedProjectedVolumeMap(retrievedContainer, volume.Name, container.Name, podName)
			if err != nil {
				return "", err
			}
			if retrievedProjectedVolumeMap == nil {
				// This should not happen, either this is an error or the flag DisableProjectedVolumes is true in VK. Building context for log.
				var retrievedProjectedVolumeMapKeys []string
				for _, retrievedProjectedVolumeMap := range retrievedContainer.ProjectedVolumeMaps {
					retrievedProjectedVolumeMapKeys = append(retrievedProjectedVolumeMapKeys, retrievedProjectedVolumeMap.Name)
				}
				log.G(ctx).Warningf("projected volumes not found %s in container %s in pod %s, current projectedVolumeMaps keys %s ."+
					"either this is an error or this is because InterLink VK has DisableProjectedVolumes set to true.",
					volume.Name, container.Name, podName, strings.Join(retrievedProjectedVolumeMapKeys, ","))
			} else {
				err = prepareMountsSimpleVolume(ctx, config, container, workingPath, *retrievedProjectedVolumeMap, volumeMount, volume, &mountedDataSB)
				if err != nil {
					return "", err
				}
			}

		case volume.Secret != nil:
			retrievedSecret, err := getRetrievedSecret(retrievedContainer, volume.Secret.SecretName, container.Name, podName)
			if err != nil {
				return "", err
			}

			err = prepareMountsSimpleVolume(ctx, config, container, workingPath, *retrievedSecret, volumeMount, volume, &mountedDataSB)
			if err != nil {
				return "", err
			}

		case volume.EmptyDir != nil:
			// retrievedContainer.EmptyDirs is deprecated in favor of each plugin giving its own emptyDir path, that will be built in mountData().
			edPath, _, err := mountData(ctx, config, container, "emptyDir", volumeMount, volume, workingPath)
			if err != nil {
				log.G(ctx).Error(err)
				return "", err
			}

			log.G(ctx).Debug("edPath: ", edPath)

			for _, mntData := range edPath {
				mountedDataSB.WriteString(mntData)
			}

		case volume.HostPath != nil:

			log.G(ctx).Info("Handling hostPath volume: ", volume.Name)

			// For hostPath volumes, we just need to bind mount the host path to the container path.
			hostPath := volume.HostPath.Path
			containerPath := volumeMount.MountPath

			if hostPath == "" || containerPath == "" {
				err := fmt.Errorf("hostPath or containerPath is empty for volume %s in pod %s", volume.Name, podName)
				log.G(ctx).Error(err)
				return "", err
			}

			if volume.Name != volumeMount.Name {
				log.G(ctx).Warningf("Volume name %s does not match volumeMount name %s in pod %s", volume.Name, volumeMount.Name, podName)
				continue
			}

			if volume.HostPath.Type != nil && *volume.HostPath.Type == v1.HostPathDirectory {
				if _, err := os.Stat(hostPath); os.IsNotExist(err) {
					err := fmt.Errorf("hostPath directory %s does not exist for volume %s in pod %s", hostPath, volume.Name, podName)
					log.G(ctx).Error(err)
					return "", err
				}
			} else if *volume.HostPath.Type == v1.HostPathDirectoryOrCreate {
				if _, err := os.Stat(hostPath); os.IsNotExist(err) {
					err = os.MkdirAll(hostPath, os.ModePerm)
					if err != nil {
						log.G(ctx).Error(err)
						return "", err
					}
				}
			} else {
				err := fmt.Errorf("unsupported hostPath type %s for volume %s in pod %s", *volume.HostPath.Type, volume.Name, podName)
				log.G(ctx).Error(err)
				return "", err
			}

			switch config.ContainerRuntime {
			case RuntimeSingularity:
				mountedDataSB.WriteString(" --bind ")
			case RuntimeEnroot:
				mountedDataSB.WriteString(" --mount ")
			}
			mountedDataSB.WriteString(hostPath + ":" + containerPath)

			// if the read-only flag is set, we add it to the mountedDataSB
			if volumeMount.ReadOnly {
				mountedDataSB.WriteString(":ro")
			}

		default:
			log.G(ctx).Warningf("Silently ignoring unknown volume type of volume: %s in pod %s", volume.Name, podName)
			return "", nil
		}
	}

	mountedData := mountedDataSB.String()
	if last := len(mountedData) - 1; last >= 0 && mountedData[last] == ',' {
		mountedData = mountedData[:last]
	}
	if len(mountedData) == 0 {
		return "", nil
	}
	log.G(ctx).Debug(mountedData)

	duration := time.Now().UnixMicro() - start
	span.AddEvent("Prepared mounts for container "+container.Name, trace.WithAttributes(
		attribute.String("peparemounts.container.name", container.Name),
		attribute.Int64("preparemounts.duration", duration),
		attribute.String("preparemounts.container.mounts", mountedData)))

	return mountedData, nil
}

// produceSLURMScript generates a SLURM script according to data collected.
// It must be called after ENVS and mounts are already set up since
// it relies on "prefix" variable being populated with needed data and ENVS passed in the commands parameter.
// It returns the path to the generated script and the first encountered error.
//
//nolint:gocyclo
func produceSLURMScript(
	ctx context.Context,
	config SlurmConfig,
	pod v1.Pod,
	path string,
	metadata metav1.ObjectMeta,
	commands []ContainerCommand,
	resourceLimits ResourceLimits,
	isDefaultCPU bool,
	isDefaultRAM bool,
	flavor *FlavorResolution,
) (string, error) {
	start := time.Now().UnixMicro()
	span := trace.SpanFromContext(ctx)
	span.AddEvent("Producing SLURM script")

	podUID := string(pod.UID)

	log.G(ctx).Info("-- Creating file for the Slurm script")
	prefix = ""
	err := os.MkdirAll(path, os.ModePerm)
	if err != nil {
		log.G(ctx).Error(err)
		return "", err
	} else {
		log.G(ctx).Info("-- Created directory " + path)
	}

	// RFC requirement: Set directory ownership if UID is configured
	// This will be applied after files are created to ensure proper ownership
	var jobUID *int64
	if config.DefaultUID != nil {
		jobUID = config.DefaultUID
	}
	if flavor != nil && flavor.UID != nil {
		jobUID = flavor.UID
	}
	if pod.Spec.SecurityContext != nil && pod.Spec.SecurityContext.RunAsUser != nil && *pod.Spec.SecurityContext.RunAsUser >= 0 {
		uid := *pod.Spec.SecurityContext.RunAsUser
		jobUID = &uid
	}

	postfix := ""

	fJob, err := os.Create(path + "/job.slurm")
	if err != nil {
		log.G(ctx).Error("Unable to create file ", path, "/job.slurm")
		log.G(ctx).Error(err)
		return "", err
	}
	defer fJob.Close()

	err = os.Chmod(path+"/job.slurm", 0774)
	if err != nil {
		log.G(ctx).Error("Unable to chmod file ", path, "/job.slurm")
		log.G(ctx).Error(err)
		return "", err
	} else {
		log.G(ctx).Debug("--- Created with correct permission file ", path, "/job.slurm")
	}

	f, err := os.Create(path + "/job.sh")
	if err != nil {
		log.G(ctx).Error("Unable to create file ", path, "/job.sh")
		log.G(ctx).Error(err)
		return "", err
	}
	defer f.Close()

	err = os.Chmod(path+"/job.sh", 0774)
	if err != nil {
		log.G(ctx).Error("Unable to chmod file ", path, "/job.sh")
		log.G(ctx).Error(err)
		return "", err
	} else {
		log.G(ctx).Debug("--- Created with correct permission file ", path, "/job.sh")
	}

	cpuLimitSetFromFlags := false
	memoryLimitSetFromFlags := false

	var sbatchFlagsFromArgo []string
	sbatchFlagsAsString := ""

	// Add flavor SLURM flags first (lowest priority)
	if flavor != nil && len(flavor.SlurmFlags) > 0 {
		log.G(ctx).Infof("Applying %d SLURM flag(s) from flavor '%s'", len(flavor.SlurmFlags), flavor.FlavorName)
		sbatchFlagsFromArgo = append(sbatchFlagsFromArgo, flavor.SlurmFlags...)
	}

	// Then process annotation flags (higher priority)
	if slurmFlags, ok := metadata.Annotations["slurm-job.vk.io/flags"]; ok {

		reCpu := regexp.MustCompile(`--cpus-per-task(?:[ =]\S+)?`)
		reRam := regexp.MustCompile(`--mem(?:[ =]\S+)?`)

		// if isDefaultCPU is false, it means that the CPU limit is set in the pod spec, so we ignore the --cpus-per-task flag from annotations.
		if !isDefaultCPU {
			if reCpu.MatchString(slurmFlags) {
				log.G(ctx).Info("Ignoring --cpus-per-task flag from annotations, since it is set already")
				slurmFlags = reCpu.ReplaceAllString(slurmFlags, "")
			}
		} else {
			if reCpu.MatchString(slurmFlags) {
				cpuLimitSetFromFlags = true
			}
		}

		if !isDefaultRAM {
			if reRam.MatchString(slurmFlags) {
				log.G(ctx).Info("Ignoring --mem flag from annotations, since it is set already")
				slurmFlags = reRam.ReplaceAllString(slurmFlags, "")
			}
		} else {
			if reRam.MatchString(slurmFlags) {
				memoryLimitSetFromFlags = true
			}
		}

		annotationFlags := strings.Split(slurmFlags, " ")
		sbatchFlagsFromArgo = append(sbatchFlagsFromArgo, annotationFlags...)
	}

	if mpiFlags, ok := metadata.Annotations["slurm-job.vk.io/mpi-flags"]; ok {
		if mpiFlags != EnvSharedFSTrue {
			mpi := append([]string{"mpiexec", "-np", "$SLURM_NTASKS"}, strings.Split(mpiFlags, " ")...)
			for _, containerCommand := range commands {
				containerCommand.runtimeCommand = append(mpi, containerCommand.runtimeCommand...)
			}
		}
	}

	// Process UID configuration with priority: pod securityContext > flavor > default
	// RFC: https://github.com/interlink-hq/interlink-slurm-plugin/discussions/58
	var uidValue *int64

	// Start with default UID from global config
	if config.DefaultUID != nil {
		uidValue = config.DefaultUID
		log.G(ctx).Debugf("Using default UID: %d", *uidValue)
	}

	// Override with flavor UID if available
	if flavor != nil && flavor.UID != nil {
		uidValue = flavor.UID
		log.G(ctx).Infof("Using UID %d from flavor '%s'", *uidValue, flavor.FlavorName)
	}

	// Override with pod securityContext.runAsUser if present (Kubernetes standard)
	if pod.Spec.SecurityContext != nil && pod.Spec.SecurityContext.RunAsUser != nil {
		runAsUser := *pod.Spec.SecurityContext.RunAsUser
		if runAsUser < 0 {
			log.G(ctx).Warningf("Invalid RunAsUser '%d' in pod securityContext (must be non-negative), ignoring", runAsUser)
		} else {
			uidValue = &runAsUser
			log.G(ctx).Infof("Using UID %d from pod spec.securityContext.runAsUser", runAsUser)
		}
	}

	// Add UID flag to sbatch if configured
	if uidValue != nil {
		sbatchFlagsFromArgo = append(sbatchFlagsFromArgo, fmt.Sprintf("--uid=%d", *uidValue))
		log.G(ctx).Infof("Setting job UID to %d", *uidValue)
	}

	// Add CPU/memory limits as flags (highest priority)
	if !isDefaultCPU {
		sbatchFlagsFromArgo = append(sbatchFlagsFromArgo, "--cpus-per-task="+strconv.FormatInt(resourceLimits.CPU, 10))
		log.G(ctx).Info("Using CPU limit of " + strconv.FormatInt(resourceLimits.CPU, 10))
	} else {
		log.G(ctx).Info("Using default CPU limit of 1")
		if !cpuLimitSetFromFlags {
			sbatchFlagsFromArgo = append(sbatchFlagsFromArgo, "--cpus-per-task=1")
		}
	}

	if !isDefaultRAM {
		sbatchFlagsFromArgo = append(sbatchFlagsFromArgo, "--mem="+strconv.FormatInt(resourceLimits.Memory/1024/1024, 10))
	} else {
		log.G(ctx).Info("Using default Memory limit of 1MB")
		if !memoryLimitSetFromFlags {
			sbatchFlagsFromArgo = append(sbatchFlagsFromArgo, "--mem=1")
		}
	}

	// Deduplicate flags - later flags override earlier ones
	// Priority order: flavor flags < annotation flags < pod spec resource flags
	sbatchFlagsFromArgo = deduplicateSlurmFlags(sbatchFlagsFromArgo)
	log.G(ctx).Debugf("Final deduplicated SLURM flags: %v", sbatchFlagsFromArgo)

	for _, slurmFlag := range sbatchFlagsFromArgo {
		if slurmFlag != "" {
			sbatchFlagsAsString += "\n#SBATCH " + slurmFlag
		}
	}

	if config.Tsocks {
		log.G(ctx).Debug("--- Adding SSH connection and setting ENVs to use TSOCKS")
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

	if podIP, ok := metadata.Annotations["interlink.eu/pod-ip"]; ok {
		prefix += "\n" + "export POD_IP=" + podIP + "\n"
	}

	if config.Commandprefix != "" {
		prefix += "\n" + config.Commandprefix
	}

	if wstunnelClientCommands, ok := metadata.Annotations["interlink.eu/wstunnel-client-commands"]; ok {
		prefix += "\n" + wstunnelClientCommands + "\n"
	}

	if preExecAnnotations, ok := metadata.Annotations["slurm-job.vk.io/pre-exec"]; ok {
		// Check if pre-exec contains a heredoc that creates mesh.sh
		if strings.Contains(preExecAnnotations, "cat <<'EOFMESH' > $TMPDIR/mesh.sh") {
			// Extract the heredoc content
			meshScript, err := extractHeredoc(preExecAnnotations, "EOFMESH")
			if err == nil && meshScript != "" {

				meshPath := filepath.Join(path, "mesh.sh")
				// #nosec G306 - intentional file mode for executable script
				err := os.WriteFile(meshPath, []byte(meshScript), 0755)
				if err != nil {
					prefix += "\n" + preExecAnnotations
				} else {
					// wrote mesh.sh, now add pre-exec without the mesh.sh heredoc
					preExecWithoutHeredoc := removeHeredoc(preExecAnnotations, "EOFMESH")
					prefix += "\n" + preExecWithoutHeredoc + "\n" + fmt.Sprintf(" %s", meshPath)
				}

				err = os.Chmod(path+"/mesh.sh", 0774)
				if err != nil {
					log.G(ctx).Error("Unable to chmod file ", path, "/job.sh")
					log.G(ctx).Error(err)
					return "", err
				} else {
					log.G(ctx).Debug("--- Created with correct permission file ", path, "/job.sh")
				}
			} else {
				// Could not extract heredoc, include as-is
				prefix += "\n" + preExecAnnotations
			}
		} else {
			// No heredoc pattern, include pre-exec as-is
			prefix += "\n" + preExecAnnotations
		}
	}

	sbatch_macros := "#!" + config.BashPath +
		"\n#SBATCH --job-name=" + podUID +
		"\n#SBATCH --output=" + path + "/job.out" +
		sbatchFlagsAsString +
		"\n" +
		prefix + " " + f.Name() +
		"\n"

	log.G(ctx).Debug("--- Writing SLURM sbatch file")

	var jobStringToBeWritten strings.Builder
	var stringToBeWritten strings.Builder

	jobStringToBeWritten.WriteString(sbatch_macros)
	_, err = fJob.WriteString(jobStringToBeWritten.String())
	if err != nil {
		log.G(ctx).Error(err)
		return "", err
	} else {
		log.G(ctx).Debug("---- Written job.slurm file")
	}

	sbatch_common_funcs_macros := `

####
# Functions
####

# Wait for 60 times 2s if the file exist. The file can be a directory or symlink or anything.
waitFileExist() {
  filePath="$1"
  printf "%s\n" "$(date -Is --utc) Checking if file exists: ${filePath} ..."
  i=1
  iMax=60
  while test "${i}" -le "${iMax}" ; do
	if test -e "${filePath}" ; then
	  printf "%s\n" "$(date -Is --utc) attempt ${i}/${iMax} file found ${filePath}"
	  break
	fi
    printf "%s\n" "$(date -Is --utc) attempt ${i}/${iMax} file not found ${filePath}"
	i=$((i + 1))
    sleep 2
  done
}

runInitCtn() {
  ctn="$1"
  shift
  printf "%s\n" "$(date -Is --utc) Running init container ${ctn}..."
  time ( "$@" ) &> ${workingPath}/init-${ctn}.out
  exitCode="$?"
  printf "%s\n" "${exitCode}" > ${workingPath}/init-${ctn}.status
  waitFileExist "${workingPath}/init-${ctn}.status"
  if test "${exitCode}" != 0 ; then
    printf "%s\n" "$(date -Is --utc) InitContainer ${ctn} failed with status ${exitCode}" >&2
    # InitContainers are fail-fast.
    exit "${exitCode}"
  fi
}

runCtn() {
  ctn="$1"
  shift
  # This subshell below is NOT POSIX shell compatible, it needs for example bash.
  time ( "$@" ) &> ${workingPath}/run-${ctn}.out &
  pid="$!"
  printf "%s\n" "$(date -Is --utc) Running in background ${ctn} pid ${pid}..."
  pidCtns="${pidCtns} ${pid}:${ctn}"
}

waitCtns() {
  # POSIX shell substring test below. Also, container name follows DNS pattern (hyphen alphanumeric, so no ":" inside)
  # pidCtn=12345:container-name-rfc-dns
  # ${pidCtn%:*} => 12345
  # ${pidCtn#*:} => container-name-rfc-dns
  for pidCtn in ${pidCtns} ; do
    pid="${pidCtn%:*}"
    ctn="${pidCtn#*:}"
    printf "%s\n" "$(date -Is --utc) Waiting for container ${ctn} pid ${pid}..."
    wait "${pid}"
    exitCode="$?"
    printf "%s\n" "${exitCode}" > "${workingPath}/run-${ctn}.status"
    printf "%s\n" "$(date -Is --utc) Container ${ctn} pid ${pid} ended with status ${exitCode}."
	waitFileExist "${workingPath}/run-${ctn}.status"
  done
  # Compatibility with jobScript, read the result of conainer .status files
  for filestatus in $(ls *.status) ; do
    exitCode=$(cat "$filestatus")
    test "${highestExitCode}" -lt "${exitCode}" && highestExitCode="${exitCode}"
  done
}

endScript() {
  printf "%s\n" "$(date -Is --utc) End of script, highest exit code ${highestExitCode}..."
  # Deprecated the sleep in favor of checking the status file with waitFileExist (see above).
  #printf "%s\n" "$(date -Is --utc) Sleeping 30s in case of..."
  # For some reason, the status files does not have the time for being written in some HPC, because slurm kills the job too soon.
  #sleep 30

  exit "${highestExitCode}"
}

####
# Main
####

highestExitCode=0

	`
	stringToBeWritten.WriteString(sbatch_common_funcs_macros)

	// Adding tracability between pod and job ID.
	stringToBeWritten.WriteString("\nprintf '%s\n' \"This pod ")
	stringToBeWritten.WriteString(pod.Name)
	stringToBeWritten.WriteString("/")
	stringToBeWritten.WriteString(podUID)
	stringToBeWritten.WriteString(" has been submitted to SLURM node ${SLURMD_NODENAME}.\"")
	stringToBeWritten.WriteString("\nprintf '%s\n' \"To get more info, please run: scontrol show job ${SLURM_JOBID}.\"")

	// Adding the workingPath as variable.
	stringToBeWritten.WriteString("\nexport workingPath=")
	stringToBeWritten.WriteString(path)
	stringToBeWritten.WriteString("\n")
	stringToBeWritten.WriteString("\nexport SANDBOX=")
	stringToBeWritten.WriteString(path)
	stringToBeWritten.WriteString("\n")

	// Generate preStop scripts if any configured
	preStopScript := generatePreStopScripts(commands, config)
	if preStopScript != "" {
		stringToBeWritten.WriteString(preStopScript)
		// Trap SIGTERM to run preStops before probe cleanup
		stringToBeWritten.WriteString("\n# Trap SIGTERM to run preStops before cleanup\ntrap 'runAllPreStops; cleanup_probes; exit' SIGTERM\n")
	}

	// Generate probe cleanup script first if any probes exist
	var hasProbes bool
	for _, containerCommand := range commands {
		if len(containerCommand.readinessProbes) > 0 || len(containerCommand.livenessProbes) > 0 || len(containerCommand.startupProbes) > 0 {
			hasProbes = true
			break
		}
	}
	if hasProbes && config.EnableProbes {
		for _, containerCommand := range commands {
			if len(containerCommand.readinessProbes) > 0 || len(containerCommand.livenessProbes) > 0 || len(containerCommand.startupProbes) > 0 {
				cleanupScript := generateProbeCleanupScript(containerCommand.containerName, containerCommand.readinessProbes, containerCommand.livenessProbes, containerCommand.startupProbes)
				stringToBeWritten.WriteString(cleanupScript)
				break // Only need one cleanup script
			}
		}
	}

	for _, containerCommand := range commands {

		stringToBeWritten.WriteString("\n")

		if config.ContainerRuntime == "enroot" {
			// Import and convert (if necessary) a container image from a specific location to an Enroot image.
			// The resulting image can be unpacked using the create command.
			// Add a custom name of the output image file (defaults to "URI.sqsh")
			// to avoid conflict with other containers in the same pod or with different pod in the same node using the same image.
			// TO DO: make a function to check if image is already present in the node.
			imageOutputName := containerCommand.containerName + podUID + ".sqsh"
			stringToBeWritten.WriteString(config.EnrootPath + " ")
			stringToBeWritten.WriteString("import " + "--output " + imageOutputName + " " + prepareImage(ctx, config, metadata, containerCommand.containerImage))
			stringToBeWritten.WriteString("\n")
			// Create container unpacking previously created image
			stringToBeWritten.WriteString(config.EnrootPath + " ")
			stringToBeWritten.WriteString("create " + "--name " + containerCommand.containerName + podUID + " " + imageOutputName)
			stringToBeWritten.WriteString("\n")
		}

		if containerCommand.isInitContainer {
			stringToBeWritten.WriteString("runInitCtn ")
		} else {
			stringToBeWritten.WriteString("runCtn ")
		}
		stringToBeWritten.WriteString(containerCommand.containerName)
		stringToBeWritten.WriteString(" ")
		stringToBeWritten.WriteString(strings.Join(containerCommand.runtimeCommand, " "))

		if containerCommand.containerCommand != nil {
			// Case the pod specified a container entrypoint array to override.
			for _, commandEntry := range containerCommand.containerCommand {
				stringToBeWritten.WriteString(" ")
				// We convert from GO array to shell command, so escaping is important to avoid space, quote issues and injection vulnerabilities.
				stringToBeWritten.WriteString(shellescape.Quote(commandEntry))
			}
		}
		if containerCommand.containerArgs != nil {
			// Case the pod specified a container command array to override.
			for _, argsEntry := range containerCommand.containerArgs {
				stringToBeWritten.WriteString(" ")
				// We convert from GO array to shell command, so escaping is important to avoid space, quote issues and injection vulnerabilities.
				stringToBeWritten.WriteString(shellescape.Quote(argsEntry))
			}
		}

		// Generate probe scripts if enabled and not an init container
		if config.EnableProbes && !containerCommand.isInitContainer && (len(containerCommand.readinessProbes) > 0 || len(containerCommand.livenessProbes) > 0 || len(containerCommand.startupProbes) > 0) {
			// Extract the image name from the singularity command
			var imageName string
			for i, arg := range containerCommand.runtimeCommand {
				if strings.HasPrefix(arg, config.ImagePrefix) || strings.HasPrefix(arg, "/") {
					imageName = arg
					break
				}
				// Look for image after singularity run/exec command
				if (arg == "run" || arg == "exec") && i+1 < len(containerCommand.runtimeCommand) {
					// Skip any options and find the image
					for j := i + 1; j < len(containerCommand.runtimeCommand); j++ {
						nextArg := containerCommand.runtimeCommand[j]
						if !strings.HasPrefix(nextArg, "-") && (strings.HasPrefix(nextArg, config.ImagePrefix) || strings.HasPrefix(nextArg, "/")) {
							imageName = nextArg
							break
						}
					}
					break
				}
			}

			if imageName != "" {
				// Store probe metadata for status checking
				err := storeProbeMetadata(path, containerCommand.containerName, len(containerCommand.readinessProbes), len(containerCommand.livenessProbes), len(containerCommand.startupProbes))
				if err != nil {
					log.G(ctx).Error("Failed to store probe metadata: ", err)
				}

				probeScript := generateProbeScript(ctx, config, containerCommand.containerName, imageName, containerCommand.readinessProbes, containerCommand.livenessProbes, containerCommand.startupProbes)
				stringToBeWritten.WriteString("\n")
				stringToBeWritten.WriteString(probeScript)
			}
		}
	}

	stringToBeWritten.WriteString("\n")
	stringToBeWritten.WriteString(postfix)

	// Waits for all containers to end, then exit with the highest exit code.
	stringToBeWritten.WriteString("\nwaitCtns\nendScript\n\n")

	_, err = f.WriteString(stringToBeWritten.String())

	if err != nil {
		log.G(ctx).Error(err)
		return "", err
	} else {
		log.G(ctx).Debug("---- Written job.sh file")
	}

	// RFC requirement: Set file and directory ownership if UID is configured
	// This allows the SLURM job to run as the specified user
	if jobUID != nil {
		uid := int(*jobUID)
		gid := -1 // -1 means don't change group ownership

		// Change ownership of the job directory and all its contents
		if err := os.Chown(path, uid, gid); err != nil {
			log.G(ctx).Warningf("Failed to chown directory %s to UID %d: %v", path, uid, err)
		} else {
			log.G(ctx).Debugf("Changed ownership of %s to UID %d", path, uid)
		}

		// Change ownership of job.slurm
		if err := os.Chown(path+"/job.slurm", uid, gid); err != nil {
			log.G(ctx).Warningf("Failed to chown %s/job.slurm to UID %d: %v", path, uid, err)
		}

		// Change ownership of job.sh
		if err := os.Chown(path+"/job.sh", uid, gid); err != nil {
			log.G(ctx).Warningf("Failed to chown %s/job.sh to UID %d: %v", path, uid, err)
		}

		log.G(ctx).Infof("Set ownership of job files to UID %d", uid)
	}

	duration := time.Now().UnixMicro() - start
	span.AddEvent("Produced SLURM script", trace.WithAttributes(
		attribute.String("produceslurmscript.path", fJob.Name()),
		attribute.Int64("preparemounts.duration", duration),
	))

	return fJob.Name(), nil
}

// SLURMBatchSubmit submits the job provided in the path argument to the SLURM queue.
// At this point, it's up to the SLURM scheduler to manage the job.
// Returns the output of the sbatch command and the first encoundered error.
func SLURMBatchSubmit(ctx context.Context, config SlurmConfig, path string) (string, error) {
	log.G(ctx).Info("- Submitting Slurm job")
	shell := exec2.ExecTask{
		Command: "sh",
		Args:    []string{"-c", "\"" + config.Sbatchpath + " " + path + "\""},
		Shell:   true,
	}

	execReturn, err := shell.Execute()
	if err != nil {
		log.G(ctx).Error("Unable to create file " + path)
		return "", err
	}
	execReturn.Stdout = strings.ReplaceAll(execReturn.Stdout, "\n", "")

	if execReturn.Stderr != "" {
		log.G(ctx).Error("Could not run sbatch: " + execReturn.Stderr)
		return "", errors.New(execReturn.Stderr)
	} else {
		log.G(ctx).Debug("Job submitted")
	}
	return string(execReturn.Stdout), nil
}

// handleJidAndPodUid creates a JID file to store the Job ID of the submitted job.
// The output parameter must be the output of SLURMBatchSubmit function and the path
// is the path where to store the JID file.
// It also adds the JID to the JIDs main structure.
// Finally, it stores the namespace and podUID info in the same location, to restore
// status at startup.
// Return the first encountered error.
func handleJidAndPodUid(ctx context.Context, pod v1.Pod, jids *map[string]*JidStruct, output string, path string) (string, error) {
	r := regexp.MustCompile(`Submitted batch job (?P<jid>\d+)`)
	jid := r.FindStringSubmatch(output)
	fJID, err := os.Create(path + "/JobID.jid")
	if err != nil {
		log.G(ctx).Error("Can't create jid_file")
		return "", err
	}
	defer fJID.Close()

	fNS, err := os.Create(path + "/PodNamespace.ns")
	if err != nil {
		log.G(ctx).Error("Can't create namespace_file")
		return "", err
	}
	defer fNS.Close()

	fUID, err := os.Create(path + "/PodUID.uid")
	if err != nil {
		log.G(ctx).Error("Can't create PodUID_file")
		return "", err
	}
	defer fUID.Close()

	_, err = fJID.WriteString(jid[1])
	if err != nil {
		log.G(ctx).Error(err)
		return "", err
	}

	(*jids)[string(pod.UID)] = &JidStruct{PodUID: string(pod.UID), PodNamespace: pod.Namespace, JID: jid[1]}
	log.G(ctx).Info("Job ID is: " + (*jids)[string(pod.UID)].JID)

	_, err = fNS.WriteString(pod.Namespace)
	if err != nil {
		log.G(ctx).Error(err)
		return "", err
	}

	_, err = fUID.WriteString(string(pod.UID))
	if err != nil {
		log.G(ctx).Error(err)
		return "", err
	}

	return (*jids)[string(pod.UID)].JID, nil
}

// removeJID delete a JID from the structure
func removeJID(podUID string, jids *map[string]*JidStruct) {
	delete(*jids, podUID)
}

// deleteContainer checks if a Job has not yet been deleted and, in case, calls the scancel command to abort the job execution.
// It then removes the JID from the main JIDs structure and all the related files on the disk.
// Returns the first encountered error.
func deleteContainer(ctx context.Context, config SlurmConfig, podUID string, jids *map[string]*JidStruct, path string) error {
	log.G(ctx).Info("- Deleting Job for pod " + podUID)
	span := trace.SpanFromContext(ctx)
	if checkIfJidExists(ctx, jids, podUID) {
		// #nosec G204 - scancel path is configured and trusted
		_, err := exec.Command(config.Scancelpath, (*jids)[podUID].JID).Output()
		if err != nil {
			log.G(ctx).Error(err)
			return err
		} else {
			log.G(ctx).Info("- Deleted Job ", (*jids)[podUID].JID)
		}
	}
	jid := (*jids)[podUID].JID
	removeJID(podUID, jids)

	errFirstAttempt := os.RemoveAll(path)
	span.SetAttributes(
		attribute.String("delete.pod.uid", podUID),
		attribute.String("delete.jid", jid),
	)

	if errFirstAttempt != nil {
		log.G(ctx).Debug("Attempt 1 of deletion failed, not really an error! Probably log file still opened, waiting for close... Error: ", errFirstAttempt)
		// We expect first rm of directory to possibly fail, in case for eg logs are in follow mode, so opened. The removeJID will end the follow loop,
		// maximum after the loop period of 4s. So we ignore the error and attempt a second time after being sure the loop has ended.
		time.Sleep(5 * time.Second)

		errSecondAttempt := os.RemoveAll(path)
		if errSecondAttempt != nil {
			log.G(ctx).Error("Attempt 2 of deletion failed: ", errSecondAttempt)
			span.AddEvent("Failed to delete SLURM Job " + jid + " for Pod " + podUID)
			return errSecondAttempt
		} else {
			log.G(ctx).Info("Attempt 2 of deletion succeeded!")
		}
	}
	span.AddEvent("SLURM Job " + jid + " for Pod " + podUID + " successfully deleted")

	// We ignore the deletion error because it is already logged, and because InterLink can still be opening files (eg logs in follow mode).
	// Once InterLink will not use files, all files will be deleted then.
	return nil
}

// For simple volume type like configMap, secret, projectedVolumeMap.
func mountDataSimpleVolume(
	ctx context.Context,
	container *v1.Container,
	path string,
	span trace.Span,
	volumeMount v1.VolumeMount,
	mountDataFiles map[string][]byte,
	start int64,
	volumeType string,
	fileMode os.FileMode,
) ([]string, []string, error) {
	span.AddEvent("Preparing " + volumeType + " mount")

	// Slice of elements of "[host path]:[container volume mount path]"
	var volumesHostToContainerPaths []string
	var envVarNames []string

	err := os.RemoveAll(path + "/" + volumeType + "/" + volumeMount.Name)
	if err != nil {
		log.G(ctx).Error("Unable to delete root folder")
		return []string{}, nil, err
	}

	log.G(ctx).Info("--- Mounting ", volumeType, ": "+volumeMount.Name)
	podVolumeDir := filepath.Join(path, volumeType, volumeMount.Name)

	for key := range mountDataFiles {
		fullPath := filepath.Join(podVolumeDir, key)
		hexString := stringToHex(fullPath)
		mode := ""
		if volumeMount.ReadOnly {
			mode = ":ro"
		} else {
			mode = ":rw"
		}
		// fullPath += (":" + volumeMount.MountPath + "/" + key + mode + " ")
		// volumesHostToContainerPaths = append(volumesHostToContainerPaths, fullPath)

		var containerPath string
		if volumeMount.SubPath != "" {
			containerPath = volumeMount.MountPath
		} else {
			containerPath = filepath.Join(volumeMount.MountPath, key)
		}

		bind := fullPath + ":" + containerPath + mode + " "
		volumesHostToContainerPaths = append(volumesHostToContainerPaths, bind)

		if os.Getenv("SHARED_FS") != "true" {
			currentEnvVarName := string(container.Name) + "_" + volumeType + "_" + hexString
			log.G(ctx).Debug("---- Setting env " + currentEnvVarName + " to mount the file later")
			err = os.Setenv(currentEnvVarName, string(mountDataFiles[key]))
			if err != nil {
				log.G(ctx).Error("--- Shared FS disabled, unable to set ENV for ", volumeType, "key: ", key, " env name: ", currentEnvVarName)
				return []string{}, nil, err
			}
			envVarNames = append(envVarNames, currentEnvVarName)
		}
	}

	if os.Getenv("SHARED_FS") == "true" {
		log.G(ctx).Info("--- Shared FS enabled, files will be directly created before the job submission")
		err := os.MkdirAll(podVolumeDir, os.FileMode(0755)|os.ModeDir)
		if err != nil {
			return []string{}, nil, fmt.Errorf("could not create whole directory of %s root cause %w", podVolumeDir, err)
		}
		log.G(ctx).Debug("--- Created folder ", podVolumeDir)
		/*
			cmd := []string{"-p " + podVolumeDir}
			shell := exec2.ExecTask{
				Command: "mkdir",
				Args:    cmd,
				Shell:   true,
			}

			execReturn, err := shell.Execute()
			if strings.Compare(execReturn.Stdout, "") != 0 {
				log.G(ctx).Error(err)
				return []string{}, nil, err
			}
			if execReturn.Stderr != "" {
				log.G(ctx).Error(execReturn.Stderr)
				return []string{}, nil, err
			} else {
				log.G(ctx).Debug("--- Created folder " + podVolumeDir)
			}
		*/

		log.G(ctx).Debug("--- Writing ", volumeType, " files")
		for k, v := range mountDataFiles {
			// TODO: Ensure that these files are deleted in failure cases
			fullPath := filepath.Join(podVolumeDir, k)

			// mode := os.FileMode(0644)
			err := os.WriteFile(fullPath, v, fileMode)
			if err != nil {
				log.G(ctx).Errorf("Could not write %s file %s", volumeType, fullPath)
				err = os.RemoveAll(fullPath)
				if err != nil {
					log.G(ctx).Error("Unable to remove file ", fullPath)
					return []string{}, nil, err
				}
				return []string{}, nil, err
			} else {
				log.G(ctx).Debugf("--- Written %s file %s", volumeType, fullPath)
			}
		}
	}
	duration := time.Now().UnixMicro() - start
	span.AddEvent("Prepared "+volumeType+" mounts", trace.WithAttributes(
		attribute.String("mountdata.container.name", container.Name),
		attribute.Int64("mountdata.duration", duration),
		attribute.StringSlice("mountdata.container."+volumeType, volumesHostToContainerPaths)))
	return volumesHostToContainerPaths, envVarNames, nil
}

/*
mountData is called by prepareMounts and creates files and directory according to their definition in the pod structure.
The data parameter is an interface and it can be of type v1.ConfigMap, v1.Secret and string (for the empty dir).

Returns:
volumesHostToContainerPaths:

	Each path is one file (not a directory). Eg for configMap that contains one file "file1" et one "file2".
	volumesHostToContainerPaths := ["/path/to/file1:/path/container/file1:rw", "/path/to/file2:/path/container/file2:rw",]

envVarNames:

	For SHARED_FS = false mode. Each one is the environment variable name matching each item of volumesHostToContainerPaths (in the same order),
	to be used to create the files inside the container.

error:

	The first encountered error, or nil
*/
func mountData(ctx context.Context, config SlurmConfig, container *v1.Container, retrievedDataObject interface{}, volumeMount v1.VolumeMount, volume v1.Volume, path string) ([]string, []string, error) {
	span := trace.SpanFromContext(ctx)
	start := time.Now().UnixMicro()
	if config.ExportPodData {
		// for _, mountSpec := range container.VolumeMounts {
		switch retrievedDataObjectCasted := retrievedDataObject.(type) {
		case v1.ConfigMap:
			var volumeType string
			var defaultMode *int32
			if volume.ConfigMap != nil {
				volumeType = "configMaps"
				defaultMode = volume.ConfigMap.DefaultMode
			} else if volume.Projected != nil {
				volumeType = "projectedVolumeMaps"
				defaultMode = volume.Projected.DefaultMode
			}

			log.G(ctx).Debugf("in mountData() volume found: %s type: %s", volumeMount.Name, volumeType)

			// Convert map of string to map of []byte
			mountDataConfigMapsAsBytes := make(map[string][]byte)
			for key := range retrievedDataObjectCasted.Data {
				mountDataConfigMapsAsBytes[key] = []byte(retrievedDataObjectCasted.Data[key])
			}
			fileMode := os.FileMode(*defaultMode)
			return mountDataSimpleVolume(ctx, container, path, span, volumeMount, mountDataConfigMapsAsBytes, start, volumeType, fileMode)

		case v1.Secret:
			volumeType := "secrets"
			log.G(ctx).Debugf("in mountData() volume found: %s type: %s", volumeMount.Name, volumeType)

			fileMode := os.FileMode(*volume.Secret.DefaultMode)
			return mountDataSimpleVolume(ctx, container, path, span, volumeMount, retrievedDataObjectCasted.Data, start, volumeType, fileMode)

		case string:
			span.AddEvent("Preparing EmptyDirs mount")
			var edPaths []string
			if volume.EmptyDir != nil {
				log.G(ctx).Debugf("in mountData() volume found: %s type: emptyDir", volumeMount.Name)

				var edPath string
				edPath = filepath.Join(path, "emptyDirs", volume.Name)
				log.G(ctx).Info("-- Creating EmptyDir in ", edPath)
				err := os.MkdirAll(edPath, os.FileMode(0755)|os.ModeDir)
				if err != nil {
					return []string{}, nil, fmt.Errorf("could not create whole directory of %s root cause %w", edPath, err)
				}
				log.G(ctx).Debug("-- Created EmptyDir in ", edPath)
				/*
					cmd := []string{"-p " + edPath}
					shell := exec2.ExecTask{
						Command: "mkdir",
						Args:    cmd,
						Shell:   true,
					}

					_, err := shell.Execute()
					if err != nil {
						log.G(ctx).Error(err)
						return []string{}, nil, err
					} else {
						log.G(ctx).Debug("-- Created EmptyDir in ", edPath)
					}
				*/

				mode := ""
				if volumeMount.ReadOnly {
					mode = ":ro"
				} else {
					mode = ":rw"
				}
				edPath += (":" + volumeMount.MountPath + mode + " ")
				edPaths = append(edPaths, " --bind "+edPath+" ")
			}
			duration := time.Now().UnixMicro() - start
			span.AddEvent("Prepared emptydir mounts", trace.WithAttributes(
				attribute.String("mountdata.container.name", container.Name),
				attribute.Int64("mountdata.duration", duration),
				attribute.StringSlice("mountdata.container.emptydirs", edPaths)))
			return edPaths, nil, nil

		default:
			log.G(ctx).Warningf("in mountData() volume %s with unknown retrievedDataObject", volumeMount.Name)
		}
	}
	return nil, nil, nil
}

// checkIfJidExists checks if a JID is in the main JIDs struct
func checkIfJidExists(ctx context.Context, jids *map[string]*JidStruct, uid string) bool {
	span := trace.SpanFromContext(ctx)
	_, ok := (*jids)[uid]

	if ok {
		return true
	} else {
		span.AddEvent("Span for PodUID " + uid + " doesn't exist")
		return false
	}
}

// getExitCode returns the exit code read from the .status file of a specific container and returns it as an int32 number
//
//nolint:unused
func getExitCode(ctx context.Context, path string, ctName string, exitCodeMatch string, sessionContextMessage string) (int32, error) {
	statusFilePath := path + "/run-" + ctName + ".status"
	exitCode, err := os.ReadFile(statusFilePath)
	if err != nil {
		statusFilePath = path + "/init-" + ctName + ".status"
		exitCode, err = os.ReadFile(statusFilePath)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				// Case job terminated before the container script has the time to write status file (eg: canceled jobs).
				log.G(ctx).Warning(sessionContextMessage, "file ", statusFilePath, " not found despite the job being in terminal state. Workaround: using Slurm job exit code:", exitCodeMatch)

				exitCodeInt, errAtoi := strconv.Atoi(exitCodeMatch)
				if errAtoi != nil {
					errWithContext := fmt.Errorf(sessionContextMessage+"error during Atoi() of getExitCode() of file %s exitCodeMatch: %s error: %s %w", statusFilePath, exitCodeMatch, fmt.Sprintf("%#v", errAtoi), errAtoi)
					log.G(ctx).Error(errWithContext)
					return 11, errWithContext
				}
				// #nosec G306 - writing status file with controlled content
				errWriteFile := os.WriteFile(statusFilePath, []byte(exitCodeMatch), 0644)
				if errWriteFile != nil {
					errWithContext := fmt.Errorf(sessionContextMessage+"error during WriteFile() of getExitCode() of file %s error: %s %w", statusFilePath, fmt.Sprintf("%#v", errWriteFile), errWriteFile)
					log.G(ctx).Error(errWithContext)
					return 12, errWithContext
				}
				return int32(exitCodeInt), nil
			} else {
				errWithContext := fmt.Errorf(sessionContextMessage+"error during ReadFile() of getExitCode() of file %s error: %s %w", statusFilePath, fmt.Sprintf("%#v", err), err)
				return 21, errWithContext
			}
		}
	}
	exitCodeInt, err := strconv.Atoi(strings.Replace(string(exitCode), "\n", "", -1))
	if err != nil {
		log.G(ctx).Error(err)
		return 0, err
	}
	return int32(exitCodeInt), nil
}

func prepareRuntimeCommand(config SlurmConfig, container v1.Container, metadata metav1.ObjectMeta) []string {
	runtimeCommand := make([]string, 0, 1)
	switch config.ContainerRuntime {
	case "singularity":
		singularityMounts := ""
		if singMounts, ok := metadata.Annotations["slurm-job.vk.io/singularity-mounts"]; ok {
			singularityMounts = singMounts
		}

		singularityOptions := ""
		if singOpts, ok := metadata.Annotations["slurm-job.vk.io/singularity-options"]; ok {
			singularityOptions = singOpts
		}

		// See https://github.com/interlink-hq/interlink-slurm-plugin/issues/32#issuecomment-2416031030
		// singularity run will honor the entrypoint/command (if exist) in container image, while exec will override entrypoint.
		// Thus if pod command (equivalent to container entrypoint) exist, we do exec, and other case we do run
		singularityCommand := ""
		if len(container.Command) != 0 {
			singularityCommand = "exec"
		} else {
			singularityCommand = "run"
		}

		// no-eval is important so that singularity does not evaluate env var, because the shellquote has already done the safety check.
		commstr1 := []string{config.SingularityPath, singularityCommand}
		commstr1 = append(commstr1, config.SingularityDefaultOptions...)
		commstr1 = append(commstr1, singularityMounts, singularityOptions)
		runtimeCommand = commstr1
	case "enroot":
		enrootMounts := ""
		if enMounts, ok := metadata.Annotations["slurm-job.vk.io/enroot-mounts"]; ok {
			enrootMounts = enMounts
		}

		enrootOptions := ""
		if enOpts, ok := metadata.Annotations["slurm-job.vk.io/enroot-options"]; ok {
			enrootOptions = enOpts
		}

		enrootCommand := "start"
		commstr1 := []string{config.EnrootPath, enrootCommand}
		commstr1 = append(commstr1, config.EnrootDefaultOptions...)
		commstr1 = append(commstr1, enrootMounts, enrootOptions)
		runtimeCommand = commstr1
	}
	return runtimeCommand
}

func prepareImage(ctx context.Context, config SlurmConfig, metadata metav1.ObjectMeta, containerImage string) string {
	image := containerImage
	imagePrefix := config.ImagePrefix

	imagePrefixAnnotationFound := false
	if imagePrefixAnnotation, ok := metadata.Annotations["slurm-job.vk.io/image-root"]; ok {
		// This takes precedence over ImagePrefix
		imagePrefix = imagePrefixAnnotation
		imagePrefixAnnotationFound = true
	}
	log.G(ctx).Info("imagePrefix from annotation? ", imagePrefixAnnotationFound, " value: ", imagePrefix)

	// If imagePrefix begins with "/", then it must be an absolute path instead of for example docker://some/image.
	// The file should be one of https://docs.sylabs.io/guides/3.1/user-guide/cli/singularity_run.html#synopsis format.
	if strings.HasPrefix(image, "/") {
		log.G(ctx).Warningf("image set to %s is an absolute path. Prefix won't be added.", image)
	} else if !strings.HasPrefix(image, imagePrefix) {
		image = imagePrefix + containerImage
	} else {
		log.G(ctx).Warningf("imagePrefix set to %s but already present in the image name %s. Prefix won't be added.", imagePrefix, image)
	}
	return image
}
