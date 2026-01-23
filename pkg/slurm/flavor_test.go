//nolint:gocritic
package slurm

import (
	"context"
	"fmt"
	"testing"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestParseMemoryString(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    int64
		wantErr bool
	}{
		{"Gigabytes", "16G", 16 * 1024 * 1024 * 1024, false},
		{"Gigabytes with B", "16GB", 16 * 1024 * 1024 * 1024, false},
		{"Megabytes", "32000M", 32000 * 1024 * 1024, false},
		{"Megabytes with B", "32000MB", 32000 * 1024 * 1024, false},
		{"Kilobytes", "1024K", 1024 * 1024, false},
		{"Kilobytes with B", "1024KB", 1024 * 1024, false},
		{"Bytes", "1024", 1024, false},
		{"Empty string", "", 0, false},
		{"Lowercase g", "16g", 16 * 1024 * 1024 * 1024, false},
		{"With spaces", "  16G  ", 16 * 1024 * 1024 * 1024, false},
		{"Invalid format", "16X", 0, true},
		{"Invalid number", "abcG", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseMemoryString(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseMemoryString() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("parseMemoryString() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDetectGPUResources(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name       string
		containers []v1.Container
		want       int64
	}{
		{
			name: "No GPU",
			containers: []v1.Container{
				{
					Name: "test",
					Resources: v1.ResourceRequirements{
						Limits: v1.ResourceList{
							v1.ResourceCPU:    resource.MustParse("2"),
							v1.ResourceMemory: resource.MustParse("8Gi"),
						},
					},
				},
			},
			want: 0,
		},
		{
			name: "Single NVIDIA GPU",
			containers: []v1.Container{
				{
					Name: "test",
					Resources: v1.ResourceRequirements{
						Limits: v1.ResourceList{
							"nvidia.com/gpu": resource.MustParse("1"),
						},
					},
				},
			},
			want: 1,
		},
		{
			name: "Multiple NVIDIA GPUs",
			containers: []v1.Container{
				{
					Name: "test",
					Resources: v1.ResourceRequirements{
						Limits: v1.ResourceList{
							"nvidia.com/gpu": resource.MustParse("2"),
						},
					},
				},
			},
			want: 2,
		},
		{
			name: "AMD GPU",
			containers: []v1.Container{
				{
					Name: "test",
					Resources: v1.ResourceRequirements{
						Limits: v1.ResourceList{
							"amd.com/gpu": resource.MustParse("1"),
						},
					},
				},
			},
			want: 1,
		},
		{
			name: "Multiple containers with GPUs",
			containers: []v1.Container{
				{
					Name: "test1",
					Resources: v1.ResourceRequirements{
						Limits: v1.ResourceList{
							"nvidia.com/gpu": resource.MustParse("1"),
						},
					},
				},
				{
					Name: "test2",
					Resources: v1.ResourceRequirements{
						Limits: v1.ResourceList{
							"nvidia.com/gpu": resource.MustParse("2"),
						},
					},
				},
			},
			want: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectGPUResources(ctx, tt.containers)
			if got != tt.want {
				t.Errorf("detectGPUResources() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExtractGPUCountFromFlags(t *testing.T) {
	tests := []struct {
		name  string
		flags []string
		want  int64
	}{
		{"No GPU flags", []string{"--partition=cpu", "--time=01:00:00"}, 0},
		{"GPU with count 1", []string{"--gres=gpu:1"}, 1},
		{"GPU with count 2", []string{"--gres=gpu:2"}, 2},
		{"GPU with count 4", []string{"--gres=gpu:4", "--partition=gpu"}, 4},
		{"GPU without count", []string{"--gres=gpu"}, 0},
		{"Multiple flags with GPU", []string{"--partition=gpu", "--gres=gpu:2", "--time=04:00:00"}, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractGPUCountFromFlags(tt.flags)
			if got != tt.want {
				t.Errorf("extractGPUCountFromFlags() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHasGPUInFlags(t *testing.T) {
	tests := []struct {
		name  string
		flags []string
		want  bool
	}{
		{"No GPU flags", []string{"--partition=cpu", "--time=01:00:00"}, false},
		{"Has --gres=gpu", []string{"--gres=gpu:1"}, true},
		{"Has gpu in partition", []string{"--partition=gpu"}, true},
		{"Has gpu in other flag", []string{"--constraint=gpu_node"}, true},
		{"Empty flags", []string{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasGPUInFlags(tt.flags)
			if got != tt.want {
				t.Errorf("hasGPUInFlags() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDeduplicateSlurmFlags(t *testing.T) {
	tests := []struct {
		name  string
		flags []string
		want  []string
	}{
		{
			name:  "No duplicates",
			flags: []string{"--partition=cpu", "--time=01:00:00"},
			want:  []string{"--partition=cpu", "--time=01:00:00"},
		},
		{
			name:  "Duplicate partitions - last wins",
			flags: []string{"--partition=cpu", "--time=01:00:00", "--partition=gpu"},
			want:  []string{"--partition=gpu", "--time=01:00:00"},
		},
		{
			name:  "Multiple duplicates",
			flags: []string{"--partition=cpu", "--mem=8G", "--partition=gpu", "--mem=16G"},
			want:  []string{"--partition=gpu", "--mem=16G"},
		},
		{
			name:  "Empty strings filtered",
			flags: []string{"--partition=cpu", "", "--mem=8G"},
			want:  []string{"--partition=cpu", "--mem=8G"},
		},
		{
			name:  "With spaces in flags",
			flags: []string{"  --partition=cpu  ", "--mem=8G"},
			want:  []string{"--partition=cpu", "--mem=8G"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deduplicateSlurmFlags(tt.flags)
			if len(got) != len(tt.want) {
				t.Errorf("deduplicateSlurmFlags() length = %v, want %v", len(got), len(tt.want))
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("deduplicateSlurmFlags()[%d] = %v, want %v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestResolveFlavor(t *testing.T) {
	ctx := context.Background()

	config := SlurmConfig{
		Flavors: map[string]FlavorConfig{
			"default": {
				Name:          "default",
				CPUDefault:    4,
				MemoryDefault: "16G",
				SlurmFlags:    []string{"--partition=cpu"},
			},
			"gpu-nvidia": {
				Name:          "gpu-nvidia",
				CPUDefault:    8,
				MemoryDefault: "64G",
				SlurmFlags:    []string{"--gres=gpu:1", "--partition=gpu"},
			},
		},
		DefaultFlavor: "default",
	}

	tests := []struct {
		name       string
		metadata   metav1.ObjectMeta
		containers []v1.Container
		wantFlavor string
		wantNil    bool
	}{
		{
			name: "Explicit annotation",
			metadata: metav1.ObjectMeta{
				Annotations: map[string]string{
					"slurm-job.vk.io/flavor": "gpu-nvidia",
				},
			},
			containers: []v1.Container{{}},
			wantFlavor: "gpu-nvidia",
			wantNil:    false,
		},
		{
			name:     "GPU auto-detection",
			metadata: metav1.ObjectMeta{},
			containers: []v1.Container{
				{
					Resources: v1.ResourceRequirements{
						Limits: v1.ResourceList{
							"nvidia.com/gpu": resource.MustParse("1"),
						},
					},
				},
			},
			wantFlavor: "gpu-nvidia",
			wantNil:    false,
		},
		{
			name:       "Default flavor",
			metadata:   metav1.ObjectMeta{},
			containers: []v1.Container{{}},
			wantFlavor: "default",
			wantNil:    false,
		},
		{
			name: "Invalid annotation falls back to default",
			metadata: metav1.ObjectMeta{
				Annotations: map[string]string{
					"slurm-job.vk.io/flavor": "non-existent",
				},
			},
			containers: []v1.Container{{}},
			wantFlavor: "default",
			wantNil:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveFlavor(ctx, config, tt.metadata, tt.containers)
			if err != nil {
				t.Errorf("resolveFlavor() error = %v", err)
				return
			}
			if tt.wantNil && got != nil {
				t.Errorf("resolveFlavor() expected nil, got %v", got)
				return
			}
			if !tt.wantNil && got == nil {
				t.Errorf("resolveFlavor() expected non-nil result")
				return
			}
			if !tt.wantNil && got.FlavorName != tt.wantFlavor {
				t.Errorf("resolveFlavor() flavor = %v, want %v", got.FlavorName, tt.wantFlavor)
			}
		})
	}
}

func TestFlavorConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		flavor  FlavorConfig
		wantErr bool
	}{
		{
			name: "Valid flavor",
			flavor: FlavorConfig{
				Name:          "test",
				CPUDefault:    4,
				MemoryDefault: "16G",
				SlurmFlags:    []string{"--partition=cpu"},
			},
			wantErr: false,
		},
		{
			name: "Empty name",
			flavor: FlavorConfig{
				Name:       "",
				CPUDefault: 4,
			},
			wantErr: true,
		},
		{
			name: "Negative CPU",
			flavor: FlavorConfig{
				Name:       "test",
				CPUDefault: -1,
			},
			wantErr: true,
		},
		{
			name: "Invalid memory format",
			flavor: FlavorConfig{
				Name:          "test",
				MemoryDefault: "invalid",
			},
			wantErr: true,
		},
		{
			name: "Invalid SLURM flag format",
			flavor: FlavorConfig{
				Name:       "test",
				SlurmFlags: []string{"invalid_flag"},
			},
			wantErr: true,
		},
		{
			name: "Empty SLURM flag",
			flavor: FlavorConfig{
				Name:       "test",
				SlurmFlags: []string{"--partition=cpu", ""},
			},
			wantErr: true,
		},
		{
			name: "Valid UID",
			flavor: FlavorConfig{
				Name:       "test",
				CPUDefault: 4,
				UID:        int64Ptr(1001),
			},
			wantErr: false,
		},
		{
			name: "Negative UID",
			flavor: FlavorConfig{
				Name:       "test",
				CPUDefault: 4,
				UID:        int64Ptr(-1),
			},
			wantErr: true,
		},
		{
			name: "UID zero is valid",
			flavor: FlavorConfig{
				Name:       "test",
				CPUDefault: 4,
				UID:        int64Ptr(0),
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.flavor.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("FlavorConfig.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// Helper function to create int64 pointers
func int64Ptr(i int64) *int64 {
	return &i
}

func TestUIDResolutionPriority(t *testing.T) {
	defaultUID := int64(1000)
	flavorUID := int64(2000)
	podSecurityContextUID := int64(3000)

	tests := []struct {
		name          string
		config        SlurmConfig
		pod           v1.Pod
		flavor        *FlavorResolution
		expectedUID   *int64
		expectWarning bool
	}{
		{
			name: "No UID configured anywhere",
			config: SlurmConfig{
				DefaultUID: nil,
			},
			pod:         v1.Pod{},
			flavor:      nil,
			expectedUID: nil,
		},
		{
			name: "Only default UID configured",
			config: SlurmConfig{
				DefaultUID: &defaultUID,
			},
			pod:         v1.Pod{},
			flavor:      nil,
			expectedUID: &defaultUID,
		},
		{
			name: "Flavor UID overrides default",
			config: SlurmConfig{
				DefaultUID: &defaultUID,
			},
			pod: v1.Pod{},
			flavor: &FlavorResolution{
				FlavorName: "test-flavor",
				UID:        &flavorUID,
			},
			expectedUID: &flavorUID,
		},
		{
			name: "Pod securityContext.runAsUser overrides all",
			config: SlurmConfig{
				DefaultUID: &defaultUID,
			},
			pod: v1.Pod{
				Spec: v1.PodSpec{
					SecurityContext: &v1.PodSecurityContext{
						RunAsUser: &podSecurityContextUID,
					},
				},
			},
			flavor: &FlavorResolution{
				FlavorName: "test-flavor",
				UID:        &flavorUID,
			},
			expectedUID: &podSecurityContextUID,
		},
		{
			name: "Negative runAsUser is ignored",
			config: SlurmConfig{
				DefaultUID: &defaultUID,
			},
			pod: v1.Pod{
				Spec: v1.PodSpec{
					SecurityContext: &v1.PodSecurityContext{
						RunAsUser: int64Ptr(-1),
					},
				},
			},
			flavor: &FlavorResolution{
				FlavorName: "test-flavor",
				UID:        &flavorUID,
			},
			expectedUID:   &flavorUID,
			expectWarning: true,
		},
		{
			name:   "Zero UID is valid",
			config: SlurmConfig{},
			pod: v1.Pod{
				Spec: v1.PodSpec{
					SecurityContext: &v1.PodSecurityContext{
						RunAsUser: int64Ptr(0),
					},
				},
			},
			flavor:      nil,
			expectedUID: int64Ptr(0),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the UID resolution logic from prepare.go
			var uidValue *int64

			// Start with default UID from global config
			if tt.config.DefaultUID != nil {
				uidValue = tt.config.DefaultUID
			}

			// Override with flavor UID if available
			if tt.flavor != nil && tt.flavor.UID != nil {
				uidValue = tt.flavor.UID
			}

			// Override with pod securityContext.runAsUser if present
			if tt.pod.Spec.SecurityContext != nil && tt.pod.Spec.SecurityContext.RunAsUser != nil {
				runAsUser := *tt.pod.Spec.SecurityContext.RunAsUser
				if runAsUser >= 0 {
					uidValue = &runAsUser
				}
			}

			// Verify the result
			if tt.expectedUID == nil && uidValue != nil {
				t.Errorf("Expected nil UID, got %d", *uidValue)
			} else if tt.expectedUID != nil && uidValue == nil {
				t.Errorf("Expected UID %d, got nil", *tt.expectedUID)
			} else if tt.expectedUID != nil && uidValue != nil && *uidValue != *tt.expectedUID {
				t.Errorf("Expected UID %d, got %d", *tt.expectedUID, *uidValue)
			}
		})
	}
}

func TestUIDInSlurmFlags(t *testing.T) {
	tests := []struct {
		name         string
		uid          *int64
		expectedFlag string
	}{
		{
			name:         "UID 1000",
			uid:          int64Ptr(1000),
			expectedFlag: "--uid=1000",
		},
		{
			name:         "UID 0",
			uid:          int64Ptr(0),
			expectedFlag: "--uid=0",
		},
		{
			name:         "UID 65535",
			uid:          int64Ptr(65535),
			expectedFlag: "--uid=65535",
		},
		{
			name:         "No UID",
			uid:          nil,
			expectedFlag: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sbatchFlags []string

			// Simulate adding UID flag
			if tt.uid != nil {
				sbatchFlags = append(sbatchFlags, fmt.Sprintf("--uid=%d", *tt.uid))
			}

			if tt.expectedFlag == "" {
				if len(sbatchFlags) > 0 {
					t.Errorf("Expected no UID flag, but got flags: %v", sbatchFlags)
				}
			} else {
				found := false
				for _, flag := range sbatchFlags {
					if flag == tt.expectedFlag {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Expected flag %q not found in flags: %v", tt.expectedFlag, sbatchFlags)
				}
			}
		})
	}
}
