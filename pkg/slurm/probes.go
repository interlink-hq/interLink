//nolint:revive,gocritic,gocyclo,ineffassign,unconvert,goconst,staticcheck
package slurm

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/containerd/containerd/log"
	v1 "k8s.io/api/core/v1"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// translateKubernetesProbes converts Kubernetes probe specifications to internal ProbeCommand format
func translateKubernetesProbes(ctx context.Context, container v1.Container) ([]ProbeCommand, []ProbeCommand, []ProbeCommand) {
	var readinessProbes, livenessProbes, startupProbes []ProbeCommand
	span := trace.SpanFromContext(ctx)

	// Handle startup probe
	if container.StartupProbe != nil {
		probe := translateSingleProbe(ctx, container.StartupProbe)
		if probe != nil {
			startupProbes = append(startupProbes, *probe)
			span.AddEvent("Translated startup probe for container " + container.Name)
		}
	}

	// Handle readiness probe
	if container.ReadinessProbe != nil {
		probe := translateSingleProbe(ctx, container.ReadinessProbe)
		if probe != nil {
			readinessProbes = append(readinessProbes, *probe)
			span.AddEvent("Translated readiness probe for container " + container.Name)
		}
	}

	// Handle liveness probe
	if container.LivenessProbe != nil {
		probe := translateSingleProbe(ctx, container.LivenessProbe)
		if probe != nil {
			livenessProbes = append(livenessProbes, *probe)
			span.AddEvent("Translated liveness probe for container " + container.Name)
		}
	}

	return readinessProbes, livenessProbes, startupProbes
}

// translateSingleProbe converts a single Kubernetes probe to internal format
func translateSingleProbe(ctx context.Context, k8sProbe *v1.Probe) *ProbeCommand {
	if k8sProbe == nil {
		return nil
	}

	probe := &ProbeCommand{
		InitialDelaySeconds: k8sProbe.InitialDelaySeconds,
		PeriodSeconds:       k8sProbe.PeriodSeconds,
		TimeoutSeconds:      k8sProbe.TimeoutSeconds,
		SuccessThreshold:    k8sProbe.SuccessThreshold,
		FailureThreshold:    k8sProbe.FailureThreshold,
	}

	// Set defaults if not specified
	if probe.PeriodSeconds == 0 {
		probe.PeriodSeconds = 10
	}
	if probe.TimeoutSeconds == 0 {
		probe.TimeoutSeconds = 1
	}
	if probe.SuccessThreshold == 0 {
		probe.SuccessThreshold = 1
	}
	if probe.FailureThreshold == 0 {
		probe.FailureThreshold = 3
	}

	// Translate HTTP probe
	if k8sProbe.HTTPGet != nil {
		probe.Type = ProbeTypeHTTP
		probe.HTTPGetAction = &HTTPGetAction{
			Path:   k8sProbe.HTTPGet.Path,
			Port:   k8sProbe.HTTPGet.Port.IntVal,
			Host:   k8sProbe.HTTPGet.Host,
			Scheme: string(k8sProbe.HTTPGet.Scheme),
		}

		// Set defaults
		if probe.HTTPGetAction.Scheme == "" {
			probe.HTTPGetAction.Scheme = "HTTP"
		}
		if probe.HTTPGetAction.Path == "" {
			probe.HTTPGetAction.Path = "/"
		}

		return probe
	}

	// Translate Exec probe
	if k8sProbe.Exec != nil {
		probe.Type = ProbeTypeExec
		probe.ExecAction = &ExecAction{
			Command: k8sProbe.Exec.Command,
		}
		return probe
	}

	log.G(ctx).Warning("Unsupported probe type (only HTTP and Exec are supported)")
	return nil
}

// generateProbeScript generates the shell script commands for executing probes
func generateProbeScript(ctx context.Context, config SlurmConfig, containerName string, imageName string, readinessProbes []ProbeCommand, livenessProbes []ProbeCommand, startupProbes []ProbeCommand) string {
	span := trace.SpanFromContext(ctx)
	span.AddEvent("Generating probe script for container " + containerName)

	if len(readinessProbes) == 0 && len(livenessProbes) == 0 && len(startupProbes) == 0 {
		return ""
	}

	var scriptBuilder strings.Builder

	// Function definitions for probe execution
	scriptBuilder.WriteString(`
# Probe execution functions
executeHTTPProbe() {
    local scheme="$1"
    local host="$2"
    local port="$3"
    local path="$4"
    local timeout="$5"
    local container_name="$6"
    
    if [ -z "$host" ] || [ "$host" = "localhost" ] || [ "$host" = "127.0.0.1" ]; then
        host="localhost"
    fi
    
    url="${scheme,,}://${host}:${port}${path}"
    
    # Use curl outside the container
    timeout "${timeout}" curl -f -s "$url" &> /dev/null
    return $?
}

executeExecProbe() {
    local timeout="$1"
    local container_name="$2"
    shift 2
    local command=("$@")

    # Use singularity exec to run the command inside the container
    `)
	scriptBuilder.WriteString(fmt.Sprintf(`"%s" exec`, config.SingularityPath))
	for _, opt := range config.SingularityDefaultOptions {
		scriptBuilder.WriteString(fmt.Sprintf(` "%s"`, opt))
	}
	scriptBuilder.WriteString(fmt.Sprintf(` "%s" timeout "${timeout}" "${command[@]}"
    return $?
}`, imageName))

	scriptBuilder.WriteString(`
runProbe() {
    local probe_type="$1"
    local container_name="$2"
    local initial_delay="$3"
    local period="$4"
    local timeout="$5"
    local success_threshold="$6"
    local failure_threshold="$7"
    local probe_name="$8"
    local probe_index="$9"
    shift 9
    local probe_args=("$@")
    
    local probe_status_file="${workingPath}/${probe_name}-probe-${container_name}-${probe_index}.status"
    local probe_timestamp_file="${workingPath}/${probe_name}-probe-${container_name}-${probe_index}.timestamp"
    
    printf "%s\n" "$(date -Is --utc) Starting ${probe_name} probe for container ${container_name}..."
    
    # Initialize probe status as unknown
    echo "UNKNOWN" > "$probe_status_file"
    date -Is --utc > "$probe_timestamp_file"
    
    # Initial delay
    if [ "$initial_delay" -gt 0 ]; then
        printf "%s\n" "$(date -Is --utc) Waiting ${initial_delay}s before starting ${probe_name} probe..."
        sleep "$initial_delay"
    fi
    
    local consecutive_successes=0
    local consecutive_failures=0
    local probe_ready=false
    
    while true; do
        # Update timestamp before each probe attempt
        date -Is --utc > "$probe_timestamp_file"
        
        if [ "$probe_type" = "http" ]; then
            executeHTTPProbe "${probe_args[@]}" "$container_name"
        elif [ "$probe_type" = "exec" ]; then
            executeExecProbe "$timeout" "$container_name" "${probe_args[@]}"
        fi
        
        local exit_code=$?
        
        if [ $exit_code -eq 0 ]; then
            consecutive_successes=$((consecutive_successes + 1))
            consecutive_failures=0
            
            if [ $consecutive_successes -ge $success_threshold ]; then
		            if [ $probe_name = "readiness" ]; then
                     printf "%s\n" "$(date -Is --utc) ${probe_name} probe succeeded for ${container_name} for ${success_threshold} times. Container is healthy."
		            elif [ $probe_name = "liveness" ]; then
		                 # Print message only if probe was previously not ready
								     if [ "$probe_ready" = false ]; then
										       printf "%s\n" "$(date -Is --utc) ${probe_name} probe succeeded for ${container_name} for ${success_threshold} times. Container is alive."
		                 fi
		            fi
                echo "SUCCESS" > "$probe_status_file"
                probe_ready=true
		            if [ "$probe_name" = "readiness" ]; then
									# For readiness probes, once successful, we can exit the loop
									return 0
								fi
            fi
        else
            consecutive_failures=$((consecutive_failures + 1))
            consecutive_successes=0
            printf "%s\n" "$(date -Is --utc) ${probe_name} probe failed for ${container_name} (${consecutive_failures}/${failure_threshold})"
            
            # Always write failure status immediately
            echo "FAILURE" > "$probe_status_file"
            probe_ready=false
            
            if [ $consecutive_failures -ge $failure_threshold ]; then
                printf "%s\n" "$(date -Is --utc) ${probe_name} probe failed for ${container_name} after ${failure_threshold} attempts" >&2
                echo "FAILED_THRESHOLD" > "$probe_status_file"
		            if [ "$probe_name" = "readiness" ]; then
										# For readiness probes, on failure threshold, exit with error
										exit 1
		            fi
            fi
        fi
        
        sleep "$period"
    done
    
    return 0
}

shutDownContainersOnProbeFail() {
  for pidCtn in ${pidCtns} ; do
    pid="${pidCtn%:*}"
    ctn="${pidCtn#*:}"
    printf "%s\n" "$(date -Is --utc) Container ${ctn} pid ${pid} killed for failed probes."
    kill "${pid}"
    printf "%s\n" "1" > "${workingPath}/run-${ctn}.status"
	waitFileExist "${workingPath}/run-${ctn}.status"
  done
}

runStartupProbe() {
    local probe_type="$1"
    local container_name="$2"
    local initial_delay="$3"
    local period="$4"
    local timeout="$5"
    local success_threshold="$6"
    local failure_threshold="$7"
    local probe_name="$8"
    local probe_index="$9"
    shift 9
    local probe_args=("$@")

    local probe_status_file="${workingPath}/${probe_name}-probe-${container_name}-${probe_index}.status"

    printf "%s\n" "$(date -Is --utc) Starting ${probe_name} probe for container ${container_name}..."

    # Initialize probe status as running
    echo "RUNNING" > "$probe_status_file"

    # Initial delay - startup probe waits before starting
    if [ "$initial_delay" -gt 0 ]; then
        printf "%s\n" "$(date -Is --utc) Waiting ${initial_delay}s before starting ${probe_name} probe..."
        sleep "$initial_delay"
    fi

    local consecutive_successes=0
    local consecutive_failures=0

    while true; do
        if [ "$probe_type" = "http" ]; then
            executeHTTPProbe "${probe_args[@]}" "$container_name"
        elif [ "$probe_type" = "exec" ]; then
            executeExecProbe "$timeout" "$container_name" "${probe_args[@]}"
        fi

        local exit_code=$?

        if [ $exit_code -eq 0 ]; then
            consecutive_successes=$((consecutive_successes + 1))
            consecutive_failures=0
            printf "%s\n" "$(date -Is --utc) ${probe_name} probe succeeded for ${container_name} (${consecutive_successes}/${success_threshold})"

            if [ $consecutive_successes -ge $success_threshold ]; then
                printf "%s\n" "$(date -Is --utc) ${probe_name} probe successful for ${container_name} - other probes can now start"
                echo "SUCCESS" > "$probe_status_file"
                return 0
            fi
        else
            consecutive_failures=$((consecutive_failures + 1))
            consecutive_successes=0
            printf "%s\n" "$(date -Is --utc) ${probe_name} probe failed for ${container_name} (${consecutive_failures}/${failure_threshold})"

            if [ $consecutive_failures -ge $failure_threshold ]; then
                printf "%s\n" "$(date -Is --utc) ${probe_name} probe failed for ${container_name} after ${failure_threshold} attempts - container should be restarted" >&2
                echo "FAILED_THRESHOLD" > "$probe_status_file"
                exit 1
            fi
        fi

        sleep "$period"
    done
}

waitForProbes() {
		local probe_name="$1"
		local container_name="$2"
		local probe_count="$3"

    if [ "$probe_count" -eq 0 ]; then
        return 0
    fi

    printf "%s\n" "$(date -Is --utc) Waiting for ${probe_name} probes to succeed before starting other probes for ${container_name}..."

    while true; do
        local all_probes_successful=true

        for i in $(seq 0 $((probe_count - 1))); do
            local probe_status_file="${workingPath}/${probe_name}-probe-${container_name}-${i}.status"
            if [ ! -f "$probe_status_file" ]; then
                all_probes_successful=false
                break
            fi

            local status=$(cat "$probe_status_file")
            if [ "$status" != "SUCCESS" ]; then
                if [ "$status" = "FAILED_THRESHOLD" ]; then
                    printf "%s\n" "$(date -Is --utc) ${probe_name} probe failed for ${container_name} - exiting" >&2
                    return 1
                fi
                all_probes_successful=false
                break
            fi
        done

        if [ "$all_probes_successful" = true ]; then
            printf "%s\n" "$(date -Is --utc) All ${probe_name} probes successful for ${container_name} - other probes can now start"
            return 0
        fi

        sleep 1
    done
}

`)

	// Generate startup probe calls - these run in background but block other probes
	for i, probe := range startupProbes {
		probeArgs := buildProbeArgs(probe)
		containerVarName := strings.ReplaceAll(containerName, "-", "_")
		scriptBuilder.WriteString(fmt.Sprintf(`
# Startup probe %d for %s
runStartupProbe "%s" "%s" %d %d %d %d %d "startup" %d %s &
STARTUP_PROBE_%s_%d_PID=$!
`, i, containerName, probe.Type, containerName, probe.InitialDelaySeconds, probe.PeriodSeconds,
			probe.TimeoutSeconds, probe.SuccessThreshold, probe.FailureThreshold, i, probeArgs, containerVarName, i))
	}

	// Wait for startup probes before starting other probes
	if len(startupProbes) > 0 {
		scriptBuilder.WriteString(fmt.Sprintf(`
# Wait for startup probes to complete before starting readiness/liveness probes
(
    waitForProbes "startup" "%s" %d
    if [ $? -eq 0 ]; then
`, containerName, len(startupProbes)))
	} else {
		// If no startup probes, start readiness/liveness directly
		scriptBuilder.WriteString(`
(
echo "No startup probes defined, starting readiness/liveness probes directly."
			if true; then
`)
	}

	// Wait for readiness probes to complete if startup probes are defined
	// else start liveness probes directly if any
	if len(readinessProbes) > 0 {

		// Generate readiness probe calls
		for i, probe := range readinessProbes {
			probeArgs := buildProbeArgs(probe)
			containerVarName := strings.ReplaceAll(containerName, "-", "_")
			scriptBuilder.WriteString(fmt.Sprintf(`
      # Readiness probe %d for %s
			runProbe "%s" "%s" %d %d %d %d %d "readiness" %d %s &
			READINESS_PROBE_%s_%d_PID=$!
`, i, containerName, probe.Type, containerName, probe.InitialDelaySeconds, probe.PeriodSeconds,
				probe.TimeoutSeconds, probe.SuccessThreshold, probe.FailureThreshold, i, probeArgs, containerVarName, i))
		}

		scriptBuilder.WriteString(fmt.Sprintf(`
      # Wait for readiness probes to complete
		  waitForProbes "readiness" "%s" %d
		  if [ $? -eq 0 ]; then
`, containerName, len(readinessProbes)))
	} else {
		// If no readiness probes start liveness directly
		scriptBuilder.WriteString(`
			echo "No readiness probes defined, starting liveness probes directly."
			if true; then
`)
	}

	// If len of livenessProbes > 0, generate liveness probes inside the conditional block
	// else close the conditional blocks with a success message
	if len(livenessProbes) == 0 {
		scriptBuilder.WriteString(`
				printf "%s\n" "$(date -Is --utc) No liveness probes defined, all probes completed successfully for container ` + containerName + `."
			`)
	} else {
		// Generate liveness probe calls
		for i, probe := range livenessProbes {
			probeArgs := buildProbeArgs(probe)
			containerVarName := strings.ReplaceAll(containerName, "-", "_")
			scriptBuilder.WriteString(fmt.Sprintf(`
        # Liveness probe %d for %s
        runProbe "%s" "%s" %d %d %d %d %d "liveness" %d %s &
        LIVENESS_PROBE_%s_%d_PID=$!
`, i, containerName, probe.Type, containerName, probe.InitialDelaySeconds, probe.PeriodSeconds,
				probe.TimeoutSeconds, probe.SuccessThreshold, probe.FailureThreshold, i, probeArgs, containerVarName, i))
		}
	}

	scriptBuilder.WriteString(`
		  else
				printf "%s\n" "$(date -Is --utc) Readiness probes failed - not starting liveness probes" >&2
        shutDownContainersOnProbeFail
				exit 1
			fi
		else
			    printf "%s\n" "$(date -Is --utc) Startup probes failed - not starting readiness probes" >&2
			    shutDownContainersOnProbeFail
			    exit 1
		fi
) &
`)

	span.SetAttributes(
		attribute.String("probes.container.name", containerName),
		attribute.Int("probes.readiness.count", len(readinessProbes)),
		attribute.Int("probes.liveness.count", len(livenessProbes)),
		attribute.Int("probes.startup.count", len(startupProbes)),
	)

	return scriptBuilder.String()
}

// buildProbeArgs constructs the argument string for probe execution
func buildProbeArgs(probe ProbeCommand) string {
	switch probe.Type {
	case ProbeTypeHTTP:
		return fmt.Sprintf(`"%s" "%s" %d "%s" %d`,
			probe.HTTPGetAction.Scheme,
			probe.HTTPGetAction.Host,
			probe.HTTPGetAction.Port,
			probe.HTTPGetAction.Path,
			probe.TimeoutSeconds)
	case ProbeTypeExec:
		args := make([]string, len(probe.ExecAction.Command))
		for i, cmd := range probe.ExecAction.Command {
			args[i] = fmt.Sprintf(`"%s"`, cmd)
		}
		return strings.Join(args, " ")
	default:
		return ""
	}
}

// generatePreStopScripts generates per-container preStop functions and a global runner
func generatePreStopScripts(commands []ContainerCommand, config SlurmConfig) string {
	// Check if any preStop handlers exist
	has := false
	for _, c := range commands {
		if len(c.preStopHandlers) > 0 {
			has = true
			break
		}
	}
	if !has || !config.EnablePreStop {
		return ""
	}

	var sb strings.Builder
	// Configure timeout
	timeout := config.PreStopTimeoutSeconds
	if timeout <= 0 {
		timeout = 5
	}
	sb.WriteString(fmt.Sprintf("\n# PreStop handling\nPRESTOP_TIMEOUT=%d\n", timeout))

	// Generate per-container preStop functions
	for _, c := range commands {
		if len(c.preStopHandlers) == 0 {
			continue
		}
		containerVarName := strings.ReplaceAll(c.containerName, "-", "_")
		sb.WriteString(fmt.Sprintf("\nrunPreStop_%s() {\n    printf \"%%s\\n\" \"$(date -Is --utc) Running preStop for %s...\"\n", containerVarName, c.containerName))
		// Execute each handler sequentially
		for i, h := range c.preStopHandlers {
			if h.Type == ProbeTypeHTTP {
				args := buildProbeArgs(h)
				// args: "scheme" "host" port "path" timeout
				// build URL
				// We will inline the curl command from args
				parts := strings.SplitN(args, " ", 5)
				if len(parts) >= 4 {
					scheme := strings.Trim(parts[0], "\"")
					host := strings.Trim(parts[1], "\"")
					port := parts[2]
					path := strings.Trim(parts[3], "\"")
					url := fmt.Sprintf("%s://%s:%s%s", strings.ToLower(scheme), host, port, path)
					sb.WriteString(fmt.Sprintf("    printf \"%%s\\n\" \"$(date -Is --utc) Running preStop HTTP handler for %s (handler %d)...\"\n", c.containerName, i))
					sb.WriteString(fmt.Sprintf("    timeout \"${PRESTOP_TIMEOUT}\" curl -f -s %s &> /dev/null || printf \"%%s\\n\" \"$(date -Is --utc) preStop HTTP handler failed for %s (handler %d)\" >&2\n", url, c.containerName, i))
				}
			} else if h.Type == ProbeTypeExec {
				// Build exec args
				args := buildProbeArgs(h)
				sb.WriteString(fmt.Sprintf("    printf \"%%s\\n\" \"$(date -Is --utc) Running preStop Exec handler for %s (handler %d)...\"\n", c.containerName, i))
				// Compose singularity exec command
				sb.WriteString("    ")
				sb.WriteString(fmt.Sprintf("\"%s\" exec", config.SingularityPath))
				for _, opt := range config.SingularityDefaultOptions {
					sb.WriteString(fmt.Sprintf(" \"%s\"", opt))
				}
				sb.WriteString(fmt.Sprintf(" \"%s\" timeout \"${PRESTOP_TIMEOUT}\" %s || printf \"%%s\\n\" \"$(date -Is --utc) preStop Exec handler failed for %s (handler %d)\" >&2\n", c.containerImage, args, c.containerName, i))
			}
		}
		sb.WriteString("}\n")
	}

	// Generate runner that calls all preStops in order
	sb.WriteString("\nrunAllPreStops() {\n    printf \"%s\\n\" \"$(date -Is --utc) Running all preStop handlers in order...\"\n")
	for _, c := range commands {
		if len(c.preStopHandlers) == 0 {
			continue
		}
		containerVarName := strings.ReplaceAll(c.containerName, "-", "_")
		sb.WriteString(fmt.Sprintf("    printf \"%%s\\n\" \"$(date -Is --utc) Running preStop for %s...\"\n", c.containerName))
		sb.WriteString(fmt.Sprintf("    runPreStop_%s || printf \"%%s\\n\" \"$(date -Is --utc) preStop for %s failed\" >&2\n", containerVarName, c.containerName))
	}
	sb.WriteString("}\n")

	return sb.String()
}

// generateProbeCleanupScript generates cleanup commands for probe processes
func generateProbeCleanupScript(containerName string, readinessProbes []ProbeCommand, livenessProbes []ProbeCommand, startupProbes []ProbeCommand) string {
	if len(readinessProbes) == 0 && len(livenessProbes) == 0 && len(startupProbes) == 0 {
		return ""
	}

	var scriptBuilder strings.Builder
	scriptBuilder.WriteString(`
# Cleanup probe processes
cleanup_probes() {
    printf "%s\n" "$(date -Is --utc) Cleaning up probe processes..."
`)

	containerVarName := strings.ReplaceAll(containerName, "-", "_")

	// Kill readiness probes
	for i := range readinessProbes {
		scriptBuilder.WriteString(fmt.Sprintf(`    if [ ! -z "$READINESS_PROBE_%s_%d_PID" ]; then
        kill $READINESS_PROBE_%s_%d_PID 2>/dev/null || true
    fi
`, containerVarName, i, containerVarName, i))
	}

	// Kill liveness probes
	for i := range livenessProbes {
		scriptBuilder.WriteString(fmt.Sprintf(`    if [ ! -z "$LIVENESS_PROBE_%s_%d_PID" ]; then
        kill $LIVENESS_PROBE_%s_%d_PID 2>/dev/null || true
    fi
`, containerVarName, i, containerVarName, i))
	}

	// Kill startup probes
	for i := range startupProbes {
		scriptBuilder.WriteString(fmt.Sprintf(`    if [ ! -z "$STARTUP_PROBE_%s_%d_PID" ]; then
        kill $STARTUP_PROBE_%s_%d_PID 2>/dev/null || true
    fi
`, containerVarName, i, containerVarName, i))
	}

	scriptBuilder.WriteString(`}

# Set up trap to cleanup probes on exit
trap cleanup_probes EXIT
`)

	return scriptBuilder.String()
}

// ProbeStatus represents the status of a single probe
type ProbeStatus struct {
	Type      ProbeType
	Status    string // SUCCESS, FAILURE, FAILED_THRESHOLD, UNKNOWN
	Timestamp time.Time
}

// getProbeStatus reads the status of a specific probe from its status file
//
//nolint:unused
func getProbeStatus(ctx context.Context, workingPath, probeType, containerName string, probeIndex int) (*ProbeStatus, error) {
	statusFilePath := fmt.Sprintf("%s/%s-probe-%s-%d.status", workingPath, probeType, containerName, probeIndex)
	timestampFilePath := fmt.Sprintf("%s/%s-probe-%s-%d.timestamp", workingPath, probeType, containerName, probeIndex)

	// Read status
	statusBytes, err := os.ReadFile(statusFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			// Probe file doesn't exist, probe not configured or not started yet
			return &ProbeStatus{
				Type:      ProbeType(probeType),
				Status:    "UNKNOWN",
				Timestamp: time.Now(),
			}, nil
		}
		return nil, fmt.Errorf("failed to read probe status file %s: %w", statusFilePath, err)
	}

	// Read timestamp
	var timestamp time.Time
	timestampBytes, err := os.ReadFile(timestampFilePath)
	if err != nil {
		// If timestamp file doesn't exist, use current time
		timestamp = time.Now()
		log.G(ctx).Debug("Timestamp file not found for probe, using current time: ", timestampFilePath)
	} else {
		timestamp, err = time.Parse(time.RFC3339, strings.TrimSpace(string(timestampBytes)))
		if err != nil {
			log.G(ctx).Warning("Failed to parse probe timestamp, using current time: ", err)
			timestamp = time.Now()
		}
	}

	return &ProbeStatus{
		Type:      ProbeType(probeType),
		Status:    strings.TrimSpace(string(statusBytes)),
		Timestamp: timestamp,
	}, nil
}

// checkContainerReadiness evaluates if a container is ready based on its readiness probes
//
//nolint:unused
func checkContainerReadiness(ctx context.Context, config SlurmConfig, workingPath, containerName string, readinessProbeCount int) bool {
	if !config.EnableProbes || readinessProbeCount == 0 {
		// No readiness probes configured, container is ready if running
		return true
	}

	span := trace.SpanFromContext(ctx)
	allProbesSuccessful := false

	for i := 0; i < readinessProbeCount; i++ {
		probeStatus, err := getProbeStatus(ctx, workingPath, "readiness", containerName, i)
		if err != nil {
			log.G(ctx).Error("Failed to check readiness probe status: ", err)
			allProbesSuccessful = false
			continue
		}

		span.SetAttributes(attribute.String(fmt.Sprintf("readiness.probe.%d.status", i), probeStatus.Status))

		if probeStatus.Status != ProbeStatusSuccess {
			allProbesSuccessful = false
			log.G(ctx).Debugf("Readiness probe %d for container %s is not successful: %s", i, containerName, probeStatus.Status)
		} else {
			allProbesSuccessful = true
		}
	}

	span.SetAttributes(attribute.Bool("container.ready", allProbesSuccessful))
	return allProbesSuccessful
}

// checkContainerLiveness evaluates if a container is alive based on its liveness probes
//
//nolint:unused
func checkContainerLiveness(ctx context.Context, config SlurmConfig, workingPath, containerName string, livenessProbeCount int) bool {
	if !config.EnableProbes || livenessProbeCount == 0 {
		// No liveness probes configured, container is alive if running
		return true
	}

	span := trace.SpanFromContext(ctx)
	allProbesSuccessful := false

	for i := 0; i < livenessProbeCount; i++ {
		probeStatus, err := getProbeStatus(ctx, workingPath, "liveness", containerName, i)
		if err != nil {
			log.G(ctx).Error("Failed to check liveness probe status: ", err)
			allProbesSuccessful = false
			continue
		}

		span.SetAttributes(attribute.String(fmt.Sprintf("liveness.probe.%d.status", i), probeStatus.Status))

		// For liveness probes, FAILED_THRESHOLD means the container should be considered dead
		if probeStatus.Status == "FAILED_THRESHOLD" {
			allProbesSuccessful = false
			log.G(ctx).Warningf("Liveness probe %d for container %s has failed threshold: %s", i, containerName, probeStatus.Status)
			continue
		}

		// SUCCESS means the probe is healthy
		if probeStatus.Status == ProbeStatusSuccess {
			allProbesSuccessful = true
		}
	}

	span.SetAttributes(attribute.Bool("container.alive", allProbesSuccessful))
	return allProbesSuccessful
}

// checkContainerStartupComplete evaluates if a container's startup probes have completed successfully
//
//nolint:unused
func checkContainerStartupComplete(ctx context.Context, config SlurmConfig, workingPath, containerName string, startupProbeCount int) bool {
	if !config.EnableProbes || startupProbeCount == 0 {
		// No startup probes configured, startup is considered complete
		return true
	}

	span := trace.SpanFromContext(ctx)
	allProbesSuccessful := false

	for i := 0; i < startupProbeCount; i++ {
		probeStatus, err := getProbeStatus(ctx, workingPath, "startup", containerName, i)
		if err != nil {
			log.G(ctx).Error("Failed to check startup probe status: ", err)
			allProbesSuccessful = false
			continue
		}

		span.SetAttributes(attribute.String(fmt.Sprintf("startup.probe.%d.status", i), probeStatus.Status))

		// Startup probes must have status "SUCCESS" to be considered complete
		// RUNNING, FAILURE, FAILED_THRESHOLD, or UNKNOWN all mean startup is not complete
		if probeStatus.Status != "SUCCESS" {
			allProbesSuccessful = false
			log.G(ctx).Debugf("Startup probe %d for container %s is not successful: %s", i, containerName, probeStatus.Status)
		} else {
			allProbesSuccessful = true
		}
	}

	span.SetAttributes(attribute.Bool("container.startup.complete", allProbesSuccessful))
	return allProbesSuccessful
}

// storeProbeMetadata saves probe count information for later status checking
//
//nolint:unused
func storeProbeMetadata(workingPath, containerName string, readinessProbeCount, livenessProbeCount, startupProbeCount int) error {
	metadataFile := fmt.Sprintf("%s/probe-metadata-%s.txt", workingPath, containerName)
	content := fmt.Sprintf("readiness:%d\nliveness:%d\nstartup:%d", readinessProbeCount, livenessProbeCount, startupProbeCount)
	// #nosec G306 - metadata file permissions are intentionally permissive for readability
	return os.WriteFile(metadataFile, []byte(content), 0644)
}

// loadProbeMetadata loads probe count information for status checking
//
//nolint:unused
func loadProbeMetadata(workingPath, containerName string) (readinessCount, livenessCount, startupCount int, err error) {
	metadataFile := fmt.Sprintf("%s/probe-metadata-%s.txt", workingPath, containerName)
	content, err := os.ReadFile(metadataFile)
	if err != nil {
		if os.IsNotExist(err) {
			// No probe metadata file means no probes configured
			return 0, 0, 0, nil
		}
		return 0, 0, 0, err
	}

	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		parts := strings.Split(line, ":")
		if len(parts) != 2 {
			continue
		}

		var count int
		if _, err := fmt.Sscanf(parts[1], "%d", &count); err != nil {
			continue
		}

		switch parts[0] {
		case "readiness":
			readinessCount = count
		case "liveness":
			livenessCount = count
		case "startup":
			startupCount = count
		}
	}

	return readinessCount, livenessCount, startupCount, nil
}
