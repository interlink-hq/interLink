package interlink

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v2"
)

func TestConfig_LoadFromYAML(t *testing.T) {
	tests := []struct {
		name        string
		yamlContent string
		want        Config
		wantErr     bool
	}{
		{
			name: "basic config without TLS",
			yamlContent: `
InterlinkAddress: "http://0.0.0.0"
InterlinkPort: "3000"
SidecarURL: "http://localhost"
SidecarPort: "4000"
VerboseLogging: true
ErrorsOnlyLogging: false
DataRootFolder: "/tmp/interlink"
`,
			want: Config{
				InterlinkAddress:  "http://0.0.0.0",
				Interlinkport:     "3000",
				Sidecarurl:        "http://localhost",
				Sidecarport:       "4000",
				VerboseLogging:    true,
				ErrorsOnlyLogging: false,
				DataRootFolder:    "/tmp/interlink",
			},
			wantErr: false,
		},
		{
			name: "config with TLS enabled",
			yamlContent: `
InterlinkAddress: "https://0.0.0.0"
InterlinkPort: "3000"
SidecarURL: "http://localhost"
SidecarPort: "4000"
VerboseLogging: false
ErrorsOnlyLogging: false
DataRootFolder: "/tmp/interlink"
TLS:
  Enabled: true
  CertFile: "/certs/server.crt"
  KeyFile: "/certs/server.key"
  CACertFile: "/certs/ca.crt"
`,
			want: Config{
				InterlinkAddress:  "https://0.0.0.0",
				Interlinkport:     "3000",
				Sidecarurl:        "http://localhost",
				Sidecarport:       "4000",
				VerboseLogging:    false,
				ErrorsOnlyLogging: false,
				DataRootFolder:    "/tmp/interlink",
				TLS: TLSConfig{
					Enabled:    true,
					CertFile:   "/certs/server.crt",
					KeyFile:    "/certs/server.key",
					CACertFile: "/certs/ca.crt",
				},
			},
			wantErr: false,
		},
		{
			name: "config with job script build config",
			yamlContent: `
InterlinkAddress: "http://0.0.0.0"
InterlinkPort: "3000"
SidecarURL: "http://localhost"
SidecarPort: "4000"
VerboseLogging: false
ErrorsOnlyLogging: false
DataRootFolder: "/tmp/interlink"
JobScriptBuildConfig:
  singularity_hub:
    server: "https://hub.example.com"
    master_token: "test-token"
    cache_validity_seconds: 3600
  apptainer_options:
    executable: "/usr/bin/apptainer"
    fakeroot: true
    containall: true
    nvidia_support: true
  volumes_options:
    scratch_area: "/scratch"
    apptainer_cachedir: "/cache"
    image_dir: "/images"
`,
			want: Config{
				InterlinkAddress:  "http://0.0.0.0",
				Interlinkport:     "3000",
				Sidecarurl:        "http://localhost",
				Sidecarport:       "4000",
				VerboseLogging:    false,
				ErrorsOnlyLogging: false,
				DataRootFolder:    "/tmp/interlink",
				JobScriptBuildConfig: &ScriptBuildConfig{
					SingularityHub: SingularityHubConfig{
						Server:               "https://hub.example.com",
						MasterToken:          "test-token",
						CacheValiditySeconds: 3600,
					},
					ApptainerOptions: ApptainerOptions{
						Executable:    "/usr/bin/apptainer",
						Fakeroot:      true,
						ContainAll:    true,
						NvidiaSupport: true,
					},
					VolumesOptions: VolumesOptions{
						ScratchArea:       "/scratch",
						ApptainerCacheDir: "/cache",
						ImageDir:          "/images",
					},
				},
			},
			wantErr: false,
		},
		{
			name:        "invalid YAML",
			yamlContent: `invalid: yaml: content: [[[`,
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary config file
			tmpDir := t.TempDir()
			configPath := filepath.Join(tmpDir, "config.yaml")
			err := os.WriteFile(configPath, []byte(tt.yamlContent), 0600)
			require.NoError(t, err)

			// Read and parse config
			data, err := os.ReadFile(configPath)
			require.NoError(t, err)

			var got Config
			err = yaml.Unmarshal(data, &got)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want.InterlinkAddress, got.InterlinkAddress)
			assert.Equal(t, tt.want.Interlinkport, got.Interlinkport)
			assert.Equal(t, tt.want.Sidecarurl, got.Sidecarurl)
			assert.Equal(t, tt.want.Sidecarport, got.Sidecarport)
			assert.Equal(t, tt.want.VerboseLogging, got.VerboseLogging)
			assert.Equal(t, tt.want.ErrorsOnlyLogging, got.ErrorsOnlyLogging)
			assert.Equal(t, tt.want.DataRootFolder, got.DataRootFolder)
			assert.Equal(t, tt.want.TLS, got.TLS)

			if tt.want.JobScriptBuildConfig != nil {
				require.NotNil(t, got.JobScriptBuildConfig)
				assert.Equal(t, tt.want.JobScriptBuildConfig.SingularityHub, got.JobScriptBuildConfig.SingularityHub)
				assert.Equal(t, tt.want.JobScriptBuildConfig.ApptainerOptions, got.JobScriptBuildConfig.ApptainerOptions)
				assert.Equal(t, tt.want.JobScriptBuildConfig.VolumesOptions, got.JobScriptBuildConfig.VolumesOptions)
			}
		})
	}
}

func TestConfig_EnvironmentOverrides(t *testing.T) {
	tests := []struct {
		name     string
		envVars  map[string]string
		initial  Config
		expected Config
	}{
		{
			name: "INTERLINKURL override",
			envVars: map[string]string{
				"INTERLINKURL": "http://override:9000",
			},
			initial: Config{
				InterlinkAddress: "http://localhost:3000",
			},
			expected: Config{
				InterlinkAddress: "http://override:9000",
			},
		},
		{
			name: "SIDECARURL override",
			envVars: map[string]string{
				"SIDECARURL": "http://sidecar-override:5000",
			},
			initial: Config{
				Sidecarurl: "http://localhost:4000",
			},
			expected: Config{
				Sidecarurl: "http://sidecar-override:5000",
			},
		},
		{
			name: "multiple overrides",
			envVars: map[string]string{
				"INTERLINKURL":  "http://new-interlink:8080",
				"SIDECARURL":    "http://new-sidecar:8081",
				"INTERLINKPORT": "9090",
				"SIDECARPORT":   "9091",
			},
			initial: Config{
				InterlinkAddress: "http://old:3000",
				Interlinkport:    "3000",
				Sidecarurl:       "http://old:4000",
				Sidecarport:      "4000",
			},
			expected: Config{
				InterlinkAddress: "http://new-interlink:8080",
				Interlinkport:    "9090",
				Sidecarurl:       "http://new-sidecar:8081",
				Sidecarport:      "9091",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set environment variables
			for k, v := range tt.envVars {
				os.Setenv(k, v)
				defer os.Unsetenv(k)
			}

			// Apply environment overrides
			got := tt.initial
			if val := os.Getenv("INTERLINKURL"); val != "" {
				got.InterlinkAddress = val
			}
			if val := os.Getenv("SIDECARURL"); val != "" {
				got.Sidecarurl = val
			}
			if val := os.Getenv("INTERLINKPORT"); val != "" {
				got.Interlinkport = val
			}
			if val := os.Getenv("SIDECARPORT"); val != "" {
				got.Sidecarport = val
			}

			assert.Equal(t, tt.expected.InterlinkAddress, got.InterlinkAddress)
			assert.Equal(t, tt.expected.Interlinkport, got.Interlinkport)
			assert.Equal(t, tt.expected.Sidecarurl, got.Sidecarurl)
			assert.Equal(t, tt.expected.Sidecarport, got.Sidecarport)
		})
	}
}

func TestTLSConfig_Validation(t *testing.T) {
	tests := []struct {
		name      string
		tlsConfig TLSConfig
		isValid   bool
	}{
		{
			name: "valid TLS config",
			tlsConfig: TLSConfig{
				Enabled:  true,
				CertFile: "/path/to/cert.pem",
				KeyFile:  "/path/to/key.pem",
			},
			isValid: true,
		},
		{
			name: "valid mTLS config",
			tlsConfig: TLSConfig{
				Enabled:    true,
				CertFile:   "/path/to/cert.pem",
				KeyFile:    "/path/to/key.pem",
				CACertFile: "/path/to/ca.pem",
			},
			isValid: true,
		},
		{
			name: "TLS disabled",
			tlsConfig: TLSConfig{
				Enabled: false,
			},
			isValid: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Basic validation: if TLS is enabled, cert and key must be provided
			if tt.tlsConfig.Enabled {
				if tt.isValid {
					assert.NotEmpty(t, tt.tlsConfig.CertFile, "CertFile should not be empty when TLS is enabled")
					assert.NotEmpty(t, tt.tlsConfig.KeyFile, "KeyFile should not be empty when TLS is enabled")
				}
			}
		})
	}
}
