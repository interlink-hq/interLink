//nolint:revive,gocritic,gocyclo,ineffassign,unconvert,goconst,staticcheck
package slurm

import (
	"fmt"
	"strings"
)

const (
	RuntimeSingularity = "singularity"
	RuntimeEnroot      = "enroot"
	EnvSharedFSTrue    = "true"
	ProbeStatusSuccess = "SUCCESS"
)

// FlavorConfig holds the configuration for a specific flavor
type FlavorConfig struct {
	Name          string   `yaml:"Name"`
	Description   string   `yaml:"Description"`
	CPUDefault    int64    `yaml:"CPUDefault"`
	MemoryDefault string   `yaml:"MemoryDefault"` // e.g., "16G", "32000M", "1024"
	UID           *int64   `yaml:"UID"`           // Optional User ID for this flavor
	SlurmFlags    []string `yaml:"SlurmFlags"`
}

// Validate checks if the FlavorConfig is valid
func (f *FlavorConfig) Validate() error {
	if f.Name == "" {
		return fmt.Errorf("flavor Name cannot be empty")
	}

	if f.CPUDefault < 0 {
		return fmt.Errorf("flavor '%s': CPUDefault cannot be negative (got %d)", f.Name, f.CPUDefault)
	}

	if f.MemoryDefault != "" {
		// Try to parse the memory string to ensure it's valid
		if _, err := parseMemoryString(f.MemoryDefault); err != nil {
			return fmt.Errorf("flavor '%s': invalid MemoryDefault format '%s': %w", f.Name, f.MemoryDefault, err)
		}
	}

	// Validate SLURM flags format (basic check)
	for i, flag := range f.SlurmFlags {
		flag = strings.TrimSpace(flag)
		if flag == "" {
			return fmt.Errorf("flavor '%s': SLURM flag at index %d is empty", f.Name, i)
		}
		// Check if flag starts with -- or -
		if !strings.HasPrefix(flag, "--") && !strings.HasPrefix(flag, "-") {
			return fmt.Errorf("flavor '%s': SLURM flag '%s' should start with '--' or '-'", f.Name, flag)
		}
	}

	// Validate UID if set
	if f.UID != nil && *f.UID < 0 {
		return fmt.Errorf("flavor '%s': UID cannot be negative (got %d)", f.Name, *f.UID)
	}

	return nil
}

// InterLinkConfig holds the whole configuration
type SlurmConfig struct {
	VKConfigPath              string                  `yaml:"VKConfigPath"`
	Sbatchpath                string                  `yaml:"SbatchPath"`
	Scancelpath               string                  `yaml:"ScancelPath"`
	Squeuepath                string                  `yaml:"SqueuePath"`
	Sinfopath                 string                  `yaml:"SinfoPath"`
	Sidecarport               string                  `yaml:"SidecarPort"`
	Socket                    string                  `yaml:"Socket"`
	ExportPodData             bool                    `yaml:"ExportPodData"`
	Commandprefix             string                  `yaml:"CommandPrefix"`
	ImagePrefix               string                  `yaml:"ImagePrefix"`
	DataRootFolder            string                  `yaml:"DataRootFolder"`
	Namespace                 string                  `yaml:"Namespace"`
	Tsocks                    bool                    `yaml:"Tsocks"`
	Tsockspath                string                  `yaml:"TsocksPath"`
	Tsockslogin               string                  `yaml:"TsocksLoginNode"`
	BashPath                  string                  `yaml:"BashPath"`
	VerboseLogging            bool                    `yaml:"VerboseLogging"`
	ErrorsOnlyLogging         bool                    `yaml:"ErrorsOnlyLogging"`
	SingularityDefaultOptions []string                `yaml:"SingularityDefaultOptions"`
	SingularityPrefix         string                  `yaml:"SingularityPrefix"`
	SingularityPath           string                  `yaml:"SingularityPath"`
	EnableProbes              bool                    `yaml:"EnableProbes"`
	EnablePreStop             bool                    `yaml:"EnablePreStop"`
	PreStopTimeoutSeconds     int                     `yaml:"PreStopTimeoutSeconds" default:"5"`
	EnrootDefaultOptions      []string                `yaml:"EnrootDefaultOptions" default:"[\"--rw\"]"`
	EnrootPrefix              string                  `yaml:"EnrootPrefix"`
	EnrootPath                string                  `yaml:"EnrootPath"`
	ContainerRuntime          string                  `yaml:"ContainerRuntime" default:"singularity"` // "singularity" or "enroot"
	Flavors                   map[string]FlavorConfig `yaml:"Flavors"`
	DefaultFlavor             string                  `yaml:"DefaultFlavor"`
	DefaultUID                *int64                  `yaml:"DefaultUID"` // Optional default User ID for all jobs (RFC: https://github.com/interlink-hq/interlink-slurm-plugin/discussions/58)
}

type CreateStruct struct {
	PodUID string `json:"PodUID"`
	PodJID string `json:"PodJID"`
}

type ProbeType string

const (
	ProbeTypeHTTP ProbeType = "http"
	ProbeTypeExec ProbeType = "exec"
)

type ProbeCommand struct {
	Type                ProbeType
	HTTPGetAction       *HTTPGetAction
	ExecAction          *ExecAction
	InitialDelaySeconds int32
	PeriodSeconds       int32
	TimeoutSeconds      int32
	SuccessThreshold    int32
	FailureThreshold    int32
}

type HTTPGetAction struct {
	Path   string
	Port   int32
	Host   string
	Scheme string
}

type ExecAction struct {
	Command []string
}

type ContainerCommand struct {
	containerName    string
	isInitContainer  bool
	runtimeCommand   []string
	containerCommand []string
	containerArgs    []string
	containerImage   string
	readinessProbes  []ProbeCommand
	livenessProbes   []ProbeCommand
	startupProbes    []ProbeCommand
	preStopHandlers  []ProbeCommand
}
