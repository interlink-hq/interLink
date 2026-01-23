//nolint:revive,gocritic,gocyclo,ineffassign,unconvert,goconst,staticcheck
package slurm

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/containerd/containerd/log"

	commonIL "github.com/interlink-hq/interlink/pkg/interlink"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	trace "go.opentelemetry.io/otel/trace"
)

// SubmitHandler generates and submits a SLURM batch script according to provided data.
// 1 Pod = 1 Job. If a Pod has multiple containers, every container is a line with it's parameters in the SLURM script.
func (h *SidecarHandler) SubmitHandler(w http.ResponseWriter, r *http.Request) {
	start := time.Now().UnixMicro()
	tracer := otel.Tracer("interlink-API")
	spanCtx, span := tracer.Start(h.ctx, "Create", trace.WithAttributes(
		attribute.Int64("start.timestamp", start),
	))
	defer span.End()
	defer commonIL.SetDurationSpan(start, span)

	log.G(h.ctx).Info("Slurm Sidecar: received Submit call")
	statusCode := http.StatusOK
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		statusCode = http.StatusInternalServerError
		h.handleError(spanCtx, w, statusCode, err)
		return
	}

	var data commonIL.RetrievedPodData

	// to be changed to commonIL.CreateStruct
	var returnedJID CreateStruct // returnValue
	var returnedJIDBytes []byte
	err = json.Unmarshal(bodyBytes, &data)
	if err != nil {
		statusCode = http.StatusInternalServerError
		h.handleError(spanCtx, w, http.StatusGatewayTimeout, err)
		return
	}

	containers := data.Pod.Spec.InitContainers
	containers = append(containers, data.Pod.Spec.Containers...)
	metadata := data.Pod.ObjectMeta
	filesPath := h.Config.DataRootFolder + data.Pod.Namespace + "-" + string(data.Pod.UID)

	// Resolve flavor to apply default CPU and memory
	flavor, err := resolveFlavor(spanCtx, h.Config, metadata, data.Pod.Spec.Containers)
	if err != nil {
		log.G(h.ctx).Error("Failed to resolve flavor: ", err)
		statusCode = http.StatusInternalServerError
		h.handleError(spanCtx, w, statusCode, err)
		return
	}

	var runtimeCommandPod []ContainerCommand
	var resourceLimits ResourceLimits

	isDefaultCPU := true
	isDefaultRAM := true

	cpuLimit := int64(0)
	memoryLimit := int64(0)

	for i, container := range containers {
		log.G(h.ctx).Info("- Beginning script generation for container " + container.Name)

		image := ""

		cpuLimitFloat := container.Resources.Limits.Cpu().AsApproximateFloat64()
		memoryLimitFromContainer, _ := container.Resources.Limits.Memory().AsInt64()

		cpuLimitFromContainer := int64(math.Ceil(cpuLimitFloat))

		if cpuLimitFromContainer == 0 {
			// No CPU limit specified in container, check if we should use flavor default
			if isDefaultCPU && flavor != nil && flavor.CPUDefault > 0 {
				log.G(h.ctx).Infof("Max CPU resource not set for %s. Using flavor '%s' default: %d CPU", container.Name, flavor.FlavorName, flavor.CPUDefault)
				cpuLimit = flavor.CPUDefault
			} else if isDefaultCPU {
				log.G(h.ctx).Warning(errors.New("Max CPU resource not set for " + container.Name + ". Only 1 CPU will be used"))
				cpuLimit = 1
			}
		} else {
			// Container specified CPU limit
			if cpuLimitFromContainer > cpuLimit {
				log.G(h.ctx).Info("Setting CPU limit to " + strconv.FormatInt(cpuLimitFromContainer, 10))
				cpuLimit = cpuLimitFromContainer
			}
			isDefaultCPU = false
		}

		if memoryLimitFromContainer == 0 {
			// No memory limit specified in container, check if we should use flavor default
			if isDefaultRAM && flavor != nil && flavor.MemoryDefault > 0 {
				log.G(h.ctx).Infof("Max Memory resource not set for %s. Using flavor '%s' default: %d bytes", container.Name, flavor.FlavorName, flavor.MemoryDefault)
				memoryLimit = flavor.MemoryDefault
			} else if isDefaultRAM {
				log.G(h.ctx).Warning(errors.New("Max Memory resource not set for " + container.Name + ". Only 1MB will be used"))
				memoryLimit = 1024 * 1024
			}
		} else {
			// Container specified memory limit
			if memoryLimitFromContainer > memoryLimit {
				log.G(h.ctx).Info("Setting Memory limit to " + strconv.FormatInt(memoryLimitFromContainer, 10))
				memoryLimit = memoryLimitFromContainer
			}
			isDefaultRAM = false
		}

		resourceLimits.CPU = cpuLimit
		resourceLimits.Memory = memoryLimit

		mounts, err := prepareMounts(spanCtx, h.Config, &data, &container, filesPath)
		log.G(h.ctx).Debug(mounts)
		if err != nil {
			statusCode = http.StatusInternalServerError
			h.handleError(spanCtx, w, http.StatusGatewayTimeout, err)
			os.RemoveAll(filesPath)
			return
		}

		// prepareEnvs creates a file in the working directory, that must exist. This is created at prepareMounts.
		envs := prepareEnvs(spanCtx, h.Config, data, container)
		image = prepareImage(spanCtx, h.Config, metadata, container.Image)
		commstr1 := prepareRuntimeCommand(h.Config, container, metadata)
		log.G(h.ctx).Debug("-- Appending all commands together...")
		runtimeCommand := make([]string, 0, len(commstr1)+len(envs))
		runtimeCommand = append(runtimeCommand, commstr1...)
		runtimeCommand = append(runtimeCommand, envs...)
		switch h.Config.ContainerRuntime {
		case RuntimeSingularity:
			runtimeCommand = append(runtimeCommand, mounts)
			runtimeCommand = append(runtimeCommand, image)
		case RuntimeEnroot:
			containerName := container.Name + string(data.Pod.UID)
			mounts = strings.ReplaceAll(mounts, ":ro", "")
			runtimeCommand = append(runtimeCommand, mounts)
			runtimeCommand = append(runtimeCommand, containerName)
		}

		isInit := false

		if i < len(data.Pod.Spec.InitContainers) {
			isInit = true
		}

		span.SetAttributes(
			attribute.String("job.container"+strconv.Itoa(i)+".name", container.Name),
			attribute.Bool("job.container"+strconv.Itoa(i)+".isinit", isInit),
			attribute.StringSlice("job.container"+strconv.Itoa(i)+".envs", envs),
			attribute.String("job.container"+strconv.Itoa(i)+".image", image),
			attribute.StringSlice("job.container"+strconv.Itoa(i)+".command", container.Command),
			attribute.StringSlice("job.container"+strconv.Itoa(i)+".args", container.Args),
		)

		// Process probes if enabled
		var readinessProbes, livenessProbes, startupProbes []ProbeCommand
		var preStopHandlers []ProbeCommand
		if h.Config.EnableProbes && !isInit {
			readinessProbes, livenessProbes, startupProbes = translateKubernetesProbes(spanCtx, container)
			if len(readinessProbes) > 0 || len(livenessProbes) > 0 || len(startupProbes) > 0 {
				log.G(h.ctx).Info("-- Container " + container.Name + " has probes configured")
				span.SetAttributes(
					attribute.Int("job.container"+strconv.Itoa(i)+".readiness_probes", len(readinessProbes)),
					attribute.Int("job.container"+strconv.Itoa(i)+".liveness_probes", len(livenessProbes)),
					attribute.Int("job.container"+strconv.Itoa(i)+".startup_probes", len(startupProbes)),
				)
			}
		}

		// Process preStop if enabled
		if h.Config.EnablePreStop && !isInit {
			if container.Lifecycle != nil && container.Lifecycle.PreStop != nil {
				handler := container.Lifecycle.PreStop
				var p ProbeCommand
				if handler.HTTPGet != nil {
					p = ProbeCommand{
						Type: ProbeTypeHTTP,
						HTTPGetAction: &HTTPGetAction{
							Path:   handler.HTTPGet.Path,
							Port:   handler.HTTPGet.Port.IntVal,
							Host:   handler.HTTPGet.Host,
							Scheme: string(handler.HTTPGet.Scheme),
						},
						TimeoutSeconds: int32(h.Config.PreStopTimeoutSeconds),
					}
				} else if handler.Exec != nil {
					p = ProbeCommand{
						Type:           ProbeTypeExec,
						ExecAction:     &ExecAction{Command: handler.Exec.Command},
						TimeoutSeconds: int32(h.Config.PreStopTimeoutSeconds),
					}
				}
				if p.Type != "" {
					preStopHandlers = append(preStopHandlers, p)
					span.AddEvent("Translated preStop for container " + container.Name)
				}
			}
		}

		runtimeCommandPod = append(runtimeCommandPod, ContainerCommand{
			runtimeCommand:   runtimeCommand,
			containerName:    container.Name,
			containerArgs:    container.Args,
			containerCommand: container.Command,
			isInitContainer:  isInit,
			readinessProbes:  readinessProbes,
			livenessProbes:   livenessProbes,
			startupProbes:    startupProbes,
			preStopHandlers:  preStopHandlers,
			containerImage:   image,
		})
	}

	span.SetAttributes(
		attribute.Int64("job.limits.cpu", resourceLimits.CPU),
		attribute.Int64("job.limits.memory", resourceLimits.Memory),
	)

	var path string

	if data.JobScript == "" {
		log.G(h.ctx).Info("-- No custom job script provided, generating one...")
		path, err = produceSLURMScript(spanCtx, h.Config, data.Pod, filesPath, metadata, runtimeCommandPod, resourceLimits, isDefaultCPU, isDefaultRAM, flavor)
		if err != nil {
			log.G(h.ctx).Error(err)
			os.RemoveAll(filesPath)
			return
		}
	} else {

		pathFile, err := os.Create(filesPath + "/jobScript.sh")
		if err != nil {
			log.G(h.ctx).Error("Unable to create file ", path, "/jobScript.sh")
			log.G(h.ctx).Error(err)
			span.AddEvent("Failed to submit the SLURM Job")
			h.handleError(spanCtx, w, http.StatusInternalServerError, err)
			//os.RemoveAll(filesPath)
			return
		}

		mode := os.FileMode(0770)

		// Change the file mode
		if err := os.Chmod(filesPath+"/jobScript.sh", mode); err != nil {
			panic(err)
		}

		_, err = pathFile.Write([]byte(data.JobScript))
		if err != nil {
			log.G(h.ctx).Error("Unable to write to file ", path, "/jobScript.sh")
			log.G(h.ctx).Error(err)
			span.AddEvent("Failed to submit the SLURM Job")
			h.handleError(spanCtx, w, http.StatusInternalServerError, err)
			//os.RemoveAll(filesPath)
			return
		}
		runtimeCommandPodLocal := append([]ContainerCommand{}, ContainerCommand{
			runtimeCommand:   []string{pathFile.Name()},
			containerName:    "jobScript",
			containerArgs:    []string{},
			containerCommand: []string{},
			isInitContainer:  false,
			readinessProbes:  []ProbeCommand{},
			livenessProbes:   []ProbeCommand{},
			startupProbes:    []ProbeCommand{},
			containerImage:   "n/a",
		})

		path, err = produceSLURMScript(spanCtx, h.Config, data.Pod, filesPath, metadata, runtimeCommandPodLocal, resourceLimits, isDefaultCPU, isDefaultRAM, flavor)
		if err != nil {
			log.G(h.ctx).Error(err)
			os.RemoveAll(filesPath)
			return
		}
	}

	out, err := SLURMBatchSubmit(h.ctx, h.Config, path)
	if err != nil {
		span.AddEvent("Failed to submit the SLURM Job")
		statusCode = http.StatusInternalServerError
		h.handleError(spanCtx, w, http.StatusGatewayTimeout, err)
		os.RemoveAll(filesPath)
		return
	}
	log.G(h.ctx).Info(out)
	jid, err := handleJidAndPodUid(h.ctx, data.Pod, h.JIDs, out, filesPath)
	if err != nil {
		statusCode = http.StatusInternalServerError
		h.handleError(spanCtx, w, http.StatusGatewayTimeout, err)
		os.RemoveAll(filesPath)
		err = deleteContainer(spanCtx, h.Config, string(data.Pod.UID), h.JIDs, filesPath)
		if err != nil {
			log.G(h.ctx).Error(err)
		}
		return
	}

	span.AddEvent("SLURM Job successfully submitted with ID " + jid)
	returnedJID = CreateStruct{PodUID: string(data.Pod.UID), PodJID: jid}

	returnedJIDBytes, err = json.Marshal(returnedJID)
	if err != nil {
		statusCode = http.StatusInternalServerError
		h.handleError(spanCtx, w, statusCode, err)
		return
	}

	w.WriteHeader(statusCode)

	commonIL.SetDurationSpan(start, span, commonIL.WithHTTPReturnCode(statusCode))

	if statusCode != http.StatusOK {
		_, writeErr := w.Write([]byte("Some errors occurred while creating containers. Check Slurm Sidecar's logs"))
		if writeErr != nil {
			log.G(h.ctx).Error(writeErr)
		}
	} else {
		_, writeErr := w.Write(returnedJIDBytes)
		if writeErr != nil {
			log.G(h.ctx).Error(writeErr)
		}
	}
}
