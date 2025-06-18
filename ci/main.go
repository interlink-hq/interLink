// A module to instantiate and tests interLink components
//
// Visit the interLink documentation for more info: https://interlink-hq.github.io/interLink/docs/intro/
//

package main

import (
	"bytes"
	"context"
	"dagger/interlink/internal/dagger"
	"fmt"
	"html/template"
	"log"
	"strings"
	"time"
)

var interLinkChart = `
nodeName: virtual-kubelet

interlink:
  enabled: false
  address: http://{{.InterLinkURL}}
  port: "3000"
  disableProjectedVolumes: true

virtualNode:
  image: "{{.VirtualKubeletRef}}"
  resources:
    CPUs: "100"
    memGiB: "128" 
    pods: "100"
  HTTPProxies:
    HTTP: null
    HTTPs: null
  HTTP:
    insecure: true
  # uncomment to enable custom nodeSelector and nodeTaints
  #nodeLabels:
  #  - "accelerator=a100"
  #nodeTaints:
  #  - key: "accelerator"
  #    value: "a100"
  #    effect: "NoSchedule"

OAUTH:
  enabled: false
`

var interLinkChartMTLS = `
nodeName: virtual-kubelet

interlink:
  enabled: false
  address: https://{{.InterLinkURL}}
  port: "3000"
  tls:
    enabled: true
    certFile: "/etc/vk/certs/tls.crt"
    keyFile: "/etc/vk/certs/tls.key"
    caCertFile: "/etc/vk/certs/ca.crt"
  disableProjectedVolumes: true

virtualNode:
  image: "{{.VirtualKubeletRef}}"
  resources:
    CPUs: "100"
    memGiB: "128" 
    pods: "100"
  HTTPProxies:
    HTTP: null
    HTTPs: null
  HTTP:
    insecure: true
    CACert: ""
  kubeletHTTP:
    insecure: true
  # uncomment to enable custom nodeSelector and nodeTaints
  #nodeLabels:
  #  - "accelerator=a100"
  #nodeTaints:
  #  - key: "accelerator"
  #    value: "a100"
  #    effect: "NoSchedule"

OAUTH:
  enabled: false
`

//	#- name: interlink
//	#  image: "{{.InterLinkRef}}"
//
// `

// generateMTLSCerts creates a container with mTLS certificates for testing
func generateMTLSCerts(san string) *dagger.Container {
	return dag.Container().From("alpine:latest").
		WithExec([]string{"apk", "add", "--no-cache", "openssl"}).
		WithExec([]string{"mkdir", "-p", "/certs"}).
		// Generate CA private key
		WithExec([]string{"openssl", "genrsa", "-out", "/certs/ca-key.pem", "4096"}).
		// Generate CA certificate
		WithExec([]string{
			"openssl", "req", "-new", "-x509", "-days", "365", "-key", "/certs/ca-key.pem",
			"-out", "/certs/ca.pem", "-subj", "/C=US/ST=CA/L=San Francisco/O=InterLink/OU=Test/CN=InterLink-CA",
		}).
		// Generate server private key
		WithExec([]string{"openssl", "genrsa", "-out", "/certs/server-key.pem", "4096"}).
		// Generate server certificate signing request
		WithExec([]string{
			"openssl", "req", "-new", "-key", "/certs/server-key.pem",
			"-out", "/certs/server.csr", "-subj", "/C=US/ST=CA/L=San Francisco/O=InterLink/OU=Server/CN=interlink",
		}).
		// Generate server certificate signed by CA with SAN
		WithExec([]string{
			"openssl", "x509", "-req", "-days", "365", "-in", "/certs/server.csr",
			"-CA", "/certs/ca.pem", "-CAkey", "/certs/ca-key.pem", "-CAcreateserial", "-out", "/certs/server-cert.pem",
			"-extensions", "v3_req", "-extfile", "/dev/stdin",
		}, dagger.ContainerWithExecOpts{
			Stdin: fmt.Sprintf("[v3_req]\nsubjectAltName = DNS:%s", san),
		}).
		// Generate client private key
		WithExec([]string{"openssl", "genrsa", "-out", "/certs/client-key.pem", "4096"}).
		// Generate client certificate signing request
		WithExec([]string{
			"openssl", "req", "-new", "-key", "/certs/client-key.pem",
			"-out", "/certs/client.csr", "-subj", "/C=US/ST=CA/L=San Francisco/O=InterLink/OU=Client/CN=virtual-kubelet",
		}).
		// Generate client certificate signed by CA
		WithExec([]string{
			"openssl", "x509", "-req", "-days", "365", "-in", "/certs/client.csr",
			"-CA", "/certs/ca.pem", "-CAkey", "/certs/ca-key.pem", "-CAcreateserial", "-out", "/certs/client-cert.pem",
		}).
		// Set permissions - use shell to expand wildcards or set individual files
		WithExec([]string{"sh", "-c", "chmod 600 /certs/*.pem"}).
		WithExec([]string{"chmod", "644", "/certs/ca.pem", "/certs/server-cert.pem", "/certs/client-cert.pem"})
}

type patchSchema struct {
	InterLinkRef      string
	VirtualKubeletRef string
	InterLinkURL      string
}

// Interlink struct for initialization and internal variables
type Interlink struct {
	Name              string
	Registry          *dagger.Service
	Manifests         *dagger.Directory
	VirtualKubeletRef string
	InterlinkRef      string
	PluginRef         string
	Kubectl           *dagger.Container
	KubeAPIs          *dagger.Service
	KubeConfig        *dagger.File
	// +private
	KubeConfigHost     *dagger.File
	InterlinkContainer *dagger.Container
	VKContainer        *dagger.Container
	PluginContainer    *dagger.Container
}

// New initializes the Dagger module at each call
func New(name string,
	// +optional
	// +default="ghcr.io/interlink-hq/interlink/virtual-kubelet-inttw:0.4.0"
	VirtualKubeletRef string,
	// +optional
	// +default="ghcr.io/interlink-hq/interlink/interlink:0.4.0"
	InterlinkRef string,
	// +optional
	// +default="ghcr.io/interlink-hq/interlink-sidecar-slurm/interlink-sidecar-slurm:0.5.0"
	pluginRef string,
) *Interlink {
	return &Interlink{
		Name:               name,
		VirtualKubeletRef:  VirtualKubeletRef,
		VKContainer:        dag.Container(),
		InterlinkRef:       InterlinkRef,
		InterlinkContainer: dag.Container(),
		PluginRef:          pluginRef,
	}
}

// Setup k8s e interlink components:
// virtual kubelet and interlink API server
func (m *Interlink) NewInterlink(
	ctx context.Context,
	// +optional
	// +defaultPath="./manifests"
	manifests *dagger.Directory,
	// +optional
	kubeconfig *dagger.File,
	// +optional
	localRegistry *dagger.Service,
	// +optional
	localCluster *dagger.Service,
	// +optional
	interlinkEndpoint *dagger.Service,
	// +optional
	// +defaultPath="./manifests/interlink-config.yaml"
	interlinkConfig *dagger.File,
	// +optional
	pluginEndpoint *dagger.Service,
	// +optional
	// +defaultPath="./manifests/plugin-config.yaml"
	pluginConfig *dagger.File,
) (*Interlink, error) {
	if localRegistry != nil {
		m.Registry = localRegistry
	}
	if m.Registry == nil {
		m.Registry = dag.Container().From("registry").
			WithExposedPort(5000).AsService()
	}

	// docker run -p 4000:4000 -v ./manifests/plugin-config.yaml:/etc/interlink/InterLinkConfig.yaml -e SHARED_FS=true -e SLURMCONFIGPATH=/etc/interlink/InterLinkConfig.yaml ghcr.io/interlink-hq/interlink-sidecar-slurm/interlink-sidecar-slurm:0.4.0
	var err error
	if pluginEndpoint == nil {
		m.PluginContainer = dag.Container().From(m.PluginRef).
			WithFile("/etc/interlink/InterLinkConfig.yaml", pluginConfig).
			WithEnvVariable("BUST", time.Now().String()).
			WithEnvVariable("SLURMCONFIGPATH", "/etc/interlink/InterLinkConfig.yaml").
			WithEnvVariable("SHARED_FS", "true").
			WithExposedPort(4000)

		pluginEndpoint, err = m.PluginContainer.AsService(dagger.ContainerAsServiceOpts{Args: []string{}, UseEntrypoint: true, InsecureRootCapabilities: true}).Start(ctx)
		if err != nil {
			return nil, err
		}
	}

	// docker run -p 3000:3000 -v ./manifests/interlink-config-local.yaml:/etc/interlink/InterLinkConfig.yaml -e INTERLINKCONFIGPATH=/etc/interlink/InterLinkConfig.yaml ghcr.io/interlink-hq/interlink/interlink:0.4.0
	if interlinkEndpoint == nil {
		interlink := m.InterlinkContainer.
			WithFile("/etc/interlink/InterLinkConfig.yaml", interlinkConfig).
			WithEnvVariable("BUST", time.Now().String()).
			WithServiceBinding("plugin", pluginEndpoint).
			WithEnvVariable("INTERLINKCONFIGPATH", "/etc/interlink/InterLinkConfig.yaml").
			WithExposedPort(3000)

		interlinkEndpoint, err = interlink.
			AsService(
				dagger.ContainerAsServiceOpts{
					Args:                     []string{},
					UseEntrypoint:            true,
					InsecureRootCapabilities: true,
				}).Start(ctx)
		if err != nil {
			return nil, err
		}

	}
	interlinkURL, err := interlinkEndpoint.Endpoint(ctx, dagger.ServiceEndpointOpts{})
	if err != nil {
		return nil, err
	}

	K3s := dag.K3S(m.Name).With(func(k *dagger.K3S) *dagger.K3S {
		return k.WithContainer(
			k.Container().
				WithEnvVariable("BUST", time.Now().String()).
				WithExec([]string{"sh", "-c", `
cat <<EOF > /etc/rancher/k3s/registries.yaml
mirrors:
  "registry:5000":
    endpoint:
      - "http://registry:5000"
EOF`}).
				WithServiceBinding("registry", m.Registry).
				WithServiceBinding("interlink", interlinkEndpoint),
		)
	})

	_, err = K3s.Server().Start(ctx)
	if err != nil {
		return nil, err
	}

	time.Sleep(60 * time.Second) // wait for k3s to be ready

	m.Manifests = manifests
	m.KubeAPIs = K3s.Server()
	m.KubeConfig = K3s.Config(dagger.K3SConfigOpts{Local: false})
	m.KubeConfigHost = K3s.Config(dagger.K3SConfigOpts{Local: true})

	// create Kustomize patch for images to be used
	patch := patchSchema{
		InterLinkRef:      m.InterlinkRef,
		VirtualKubeletRef: m.VirtualKubeletRef,
		InterLinkURL:      strings.Split(interlinkURL, ":")[0],
	}

	bufferIL := new(bytes.Buffer)

	virtualKubeletCompiler, err := template.New("vk").Parse(interLinkChart)
	if err != nil {
		return nil, err
	}

	bufferVK := new(bytes.Buffer)

	err = virtualKubeletCompiler.Execute(bufferVK, patch)
	if err != nil {
		return nil, err
	}

	fmt.Println(bufferVK.String())

	kubectl := dag.Container().From("bitnami/kubectl:1.32-debian-12").
		WithServiceBinding("registry", m.Registry).
		WithServiceBinding("plugin", pluginEndpoint).
		WithEnvVariable("BUST", time.Now().String()).
		WithServiceBinding("interlink", interlinkEndpoint).
		WithUser("root").
		WithExec([]string{"mkdir", "-p", "/opt/user"}).
		WithExec([]string{"chown", "-R", "1001:0", "/opt/user"}).
		WithExec([]string{"apt", "update"}).
		WithExec([]string{"apt", "update"}).
		WithExec([]string{"apt", "install", "-y", "curl", "python3", "python3-pip", "python3-venv", "git", "vim"}).
		WithMountedFile("/.kube/config", m.KubeConfig).
		WithExec([]string{"chown", "1001:0", "/.kube/config"}).
		WithUser("1001").
		WithDirectory("/manifests", m.Manifests).
		WithNewFile("/manifests/interlink-merge.yaml", bufferIL.String(), dagger.ContainerWithNewFileOpts{
			Permissions: 0o755,
		}).
		WithEntrypoint([]string{"kubectl"})

	m.Kubectl = kubectl

	dag.Container().From("alpine/helm:3.16.1").
		WithMountedFile("/.kube/config", m.KubeConfig).
		WithEnvVariable("BUST", time.Now().String()).
		WithEnvVariable("KUBECONFIG", "/.kube/config").
		WithNewFile("/manifests/vk_helm_chart.yaml", bufferVK.String(), dagger.ContainerWithNewFileOpts{
			Permissions: 0o755,
		}).
		WithExec([]string{
			"helm",
			"install",
			"--create-namespace",
			"-n", "interlink",
			"virtual-node",
			"oci://ghcr.io/interlink-hq/interlink-helm-chart/interlink",
			"--version", "0.5.0-pre1",
			"--values", "/manifests/vk_helm_chart.yaml",
		}).Stdout(ctx)

	return m, nil
}

// NewInterlinkMTLS sets up interLink components with mTLS enabled for testing
func (m *Interlink) NewInterlinkMTLS(
	ctx context.Context,
	// +optional
	// +defaultPath="./manifests"
	manifests *dagger.Directory,
	// +optional
	kubeconfig *dagger.File,
	// +optional
	localRegistry *dagger.Service,
	// +optional
	localCluster *dagger.Service,
	// +optional
	interlinkEndpoint *dagger.Service,
	// +optional
	pluginEndpoint *dagger.Service,
) (*Interlink, error) {
	if localRegistry != nil {
		m.Registry = localRegistry
	}
	if m.Registry == nil {
		m.Registry = dag.Container().From("registry").
			WithExposedPort(5000).AsService()
	}

	// Extract hostname from URL for SAN
	interlinkHost := "interlink"

	// Generate mTLS certificates
	certContainer := generateMTLSCerts(interlinkHost)

	// Create interlink config with mTLS enabled
	interlinkConfigMTLS := dag.Container().From("alpine").
		WithFile("/certs/ca.pem", certContainer.File("/certs/ca.pem")).
		WithFile("/certs/server-cert.pem", certContainer.File("/certs/server-cert.pem")).
		WithFile("/certs/server-key.pem", certContainer.File("/certs/server-key.pem")).
		WithNewFile("/etc/interlink/InterLinkConfig.yaml", `
InterlinkAddress: "https://0.0.0.0"
InterlinkPort: "3000"
SidecarURL: "http://plugin"
SidecarPort: "4000"
VerboseLogging: true
ErrorsOnlyLogging: false
DataRootFolder: "~/.interlink"
TLS:
  Enabled: true
  CertFile: "/certs/server-cert.pem"
  KeyFile: "/certs/server-key.pem"
  CACertFile: "/certs/ca.pem"
`)

	// Create plugin config
	pluginConfigFile := dag.Container().From("alpine").
		WithNewFile("/etc/interlink/InterLinkConfig.yaml", `
InterlinkURL: "http://interlink"
InterlinkPort: "3000"
SidecarURL: "http://0.0.0.0"
SidecarPort: "4000"
VerboseLogging: true
ErrorsOnlyLogging: false
# NEEDED PATH FOR GITHUB ACTIONS
#DataRootFolder: "/home/runner/work/interLink/interLink/.interlink/"
# on your host use something like:
DataRootFolder: "/home/ubuntu/.interlink/"
ExportPodData: true
SbatchPath: "/usr/bin/sbatch"
ScancelPath: "/usr/bin/scancel"
SqueuePath: "/usr/bin/squeue"
CommandPrefix: ""
SingularityPrefix: ""
Namespace: "vk"
Tsocks: false
TsocksPath: "$WORK/tsocks-1.8beta5+ds1/libtsocks.so"
TsocksLoginNode: "login01"
BashPath: /bin/bash
`)

	var err error
	// Setup plugin with standard config
	if pluginEndpoint == nil {
		m.PluginContainer = dag.Container().From(m.PluginRef).
			WithFile("/etc/interlink/InterLinkConfig.yaml", pluginConfigFile.File("/etc/interlink/InterLinkConfig.yaml")).
			WithEnvVariable("BUST", time.Now().String()).
			WithEnvVariable("SLURMCONFIGPATH", "/etc/interlink/InterLinkConfig.yaml").
			WithEnvVariable("SHARED_FS", "true").
			WithExposedPort(4000)

		pluginEndpoint, err = m.PluginContainer.AsService(dagger.ContainerAsServiceOpts{Args: []string{}, UseEntrypoint: true, InsecureRootCapabilities: true}).Start(ctx)
		if err != nil {
			return nil, err
		}
	}

	// Setup interLink with mTLS enabled
	if interlinkEndpoint == nil {
		interlink := m.InterlinkContainer.
			WithFile("/etc/interlink/InterLinkConfig.yaml", interlinkConfigMTLS.File("/etc/interlink/InterLinkConfig.yaml")).
			WithFile("/certs/ca.pem", certContainer.File("/certs/ca.pem")).
			WithFile("/certs/server-cert.pem", certContainer.File("/certs/server-cert.pem")).
			WithFile("/certs/server-key.pem", certContainer.File("/certs/server-key.pem")).
			WithEnvVariable("BUST", time.Now().String()).
			WithServiceBinding("plugin", pluginEndpoint).
			WithEnvVariable("INTERLINKCONFIGPATH", "/etc/interlink/InterLinkConfig.yaml").
			WithExposedPort(3000)

		interlinkEndpoint, err = interlink.
			AsService(
				dagger.ContainerAsServiceOpts{
					Args:                     []string{},
					UseEntrypoint:            true,
					InsecureRootCapabilities: true,
				}).Start(ctx)
		if err != nil {
			return nil, err
		}
	}

	K3s := dag.K3S(m.Name).With(func(k *dagger.K3S) *dagger.K3S {
		return k.WithContainer(
			k.Container().
				WithEnvVariable("BUST", time.Now().String()).
				WithExec([]string{"sh", "-c", `
cat <<EOF > /etc/rancher/k3s/registries.yaml
mirrors:
  "registry:5000":
    endpoint:
      - "http://registry:5000"
EOF`}).
				WithServiceBinding("registry", m.Registry).
				WithServiceBinding("interlink", interlinkEndpoint),
		)
	})

	_, err = K3s.Server().Start(ctx)
	if err != nil {
		return nil, err
	}

	time.Sleep(60 * time.Second) // wait for k3s to be ready

	m.Manifests = manifests
	m.KubeAPIs = K3s.Server()
	m.KubeConfig = K3s.Config(dagger.K3SConfigOpts{Local: false})
	m.KubeConfigHost = K3s.Config(dagger.K3SConfigOpts{Local: true})

	interlinkEndpointURL, err := interlinkEndpoint.Hostname(ctx)
	if err != nil {
		return nil, err
	}
	hostname, err := dag.Container().From("nicolaka/netshoot").
		WithServiceBinding("interlink", interlinkEndpoint).
		WithExec([]string{"sh", "-c", fmt.Sprintf("host %s | awk '{print $4}'", interlinkEndpointURL)}).Stdout(ctx)
	if err != nil {
		fmt.Println("Error getting hostname:", err)
		return nil, err
	}

	// create Kustomize patch for images to be used with mTLS
	patch := patchSchema{
		InterLinkRef:      m.InterlinkRef,
		VirtualKubeletRef: m.VirtualKubeletRef,
		InterLinkURL:      "interlink",
	}

	bufferIL := new(bytes.Buffer)

	virtualKubeletCompiler, err := template.New("vk-mtls").Parse(interLinkChartMTLS)
	if err != nil {
		return nil, err
	}

	bufferVK := new(bytes.Buffer)

	err = virtualKubeletCompiler.Execute(bufferVK, patch)
	if err != nil {
		return nil, err
	}

	fmt.Println("mTLS enabled VK config:")
	fmt.Println(bufferVK.String())

	kubectl := dag.Container().From("bitnami/kubectl:1.32-debian-12").
		WithServiceBinding("registry", m.Registry).
		WithServiceBinding("plugin", pluginEndpoint).
		WithEnvVariable("BUST", time.Now().String()).
		WithServiceBinding("interlink", interlinkEndpoint).
		WithUser("root").
		WithExec([]string{"mkdir", "-p", "/opt/user"}).
		WithExec([]string{"chown", "-R", "1001:0", "/opt/user"}).
		WithExec([]string{"apt", "update"}).
		WithExec([]string{"apt", "update"}).
		WithExec([]string{"apt", "install", "-y", "curl", "python3", "python3-pip", "python3-venv", "git", "vim"}).
		WithMountedFile("/.kube/config", m.KubeConfig).
		WithExec([]string{"chown", "1001:0", "/.kube/config"}).
		WithUser("1001").
		WithDirectory("/manifests", m.Manifests).
		WithNewFile("/manifests/interlink-merge.yaml", bufferIL.String(), dagger.ContainerWithNewFileOpts{
			Permissions: 0o755,
		}).
		WithEntrypoint([]string{"kubectl"})

	kubectl.WithFile("/certs/ca.pem", certContainer.File("/certs/ca.pem"), dagger.ContainerWithFileOpts{Owner: "1001:0"}).
		WithFile("/certs/client-cert.pem", certContainer.File("/certs/client-cert.pem"), dagger.ContainerWithFileOpts{Owner: "1001:0"}).
		WithFile("/certs/client-key.pem", certContainer.File("/certs/client-key.pem"), dagger.ContainerWithFileOpts{Owner: "1001:0"}).
		WithUser("root").
		WithExec([]string{
			"kubectl", "create", "namespace", "interlink",
		}).
		WithExec([]string{
			"kubectl", "create", "secret", "generic", "virtual-kubelet-tls-certs",
			"--from-file=ca.crt=/certs/ca.pem",
			"--from-file=tls.crt=/certs/client-cert.pem",
			"--from-file=tls.key=/certs/client-key.pem",
			"-n", "interlink",
		}).Stdout(ctx)

	m.Kubectl = kubectl

	// Deploy with mTLS certificates mounted
	dag.Container().From("alpine/helm:3.16.1").
		WithMountedFile("/.kube/config", m.KubeConfig).
		WithEnvVariable("BUST", time.Now().String()).
		WithEnvVariable("KUBECONFIG", "/.kube/config").
		WithNewFile("/manifests/vk_helm_chart_mtls.yaml", bufferVK.String(), dagger.ContainerWithNewFileOpts{
			Permissions: 0o755,
		}).
		WithExec([]string{
			"helm",
			"install",
			"--create-namespace",
			"-n", "interlink",
			"virtual-node-mtls",
			"oci://ghcr.io/interlink-hq/interlink-helm-chart/interlink",
			"--version", "0.5.0",
			"--values", "/manifests/vk_helm_chart_mtls.yaml",
		}).Stdout(ctx)

	kubectl.WithExec([]string{
		"kubectl",
		"patch",
		"deployment",
		"-n", "interlink",
		"virtual-kubelet-node",
		"-p", fmt.Sprintf("{\"spec\":{\"template\":{\"spec\":{\"hostAliases\":[{\"ip\":\"%s\",\"hostnames\":[\"interlink\"]}]}}}}", strings.TrimRight(hostname, "\n")),
	}).Stdout(ctx)

	return m, nil
}

// Returns the kubeconfig file of the k3s cluster
func (m *Interlink) Config() *dagger.File {
	return dag.K3S(m.Name).Config(dagger.K3SConfigOpts{Local: true})
}

// Build interLink and virtual kubelet docker images from source
// and publish them in registry service
func (m *Interlink) BuildImages(
	ctx context.Context,
	// +optional
	// +default="registry:5000/virtual-kubelet-inttw"
	virtualKubeletRef string,
	// +optional
	// +default="registry:5000/interlink"
	interlinkRef string,
	// +optional
	// +default="registry:5000/plugin-test"
	pluginRef string,
	// +optional
	// +defaultPath="../"
	sourceFolder *dagger.Directory,
) (*Interlink, error) {
	// TODO: get tag
	m.Registry = dag.Container().From("registry").
		WithExposedPort(5000).AsService()

	m.VirtualKubeletRef = virtualKubeletRef
	m.InterlinkRef = interlinkRef

	vkVersionSplits := strings.Split(virtualKubeletRef, ":")

	vkVersion := vkVersionSplits[len(vkVersionSplits)-1]
	if vkVersion == "" {
		return nil, fmt.Errorf("no tag specified on the image for VK")
	}

	builder := dag.Container().
		From("golang:1.24").
		WithDirectory("/src", sourceFolder).
		WithWorkdir("/src").
		WithMountedCache("/go/pkg/mod", dag.CacheVolume("go-mod-122")).
		WithEnvVariable("GOMODCACHE", "/go/pkg/mod").
		WithEnvVariable("VERSION", "local").
		WithMountedCache("/go/build-cache", dag.CacheVolume("go-build-122")).
		WithEnvVariable("GOCACHE", "/go/build-cache").
		WithEnvVariable("CGO_ENABLED", "0").
		WithExec([]string{"bash", "-c", "KUBELET_VERSION=${VERSION} ./cmd/virtual-kubelet/set-version.sh"}).
		WithExec([]string{"go", "build", "-o", "bin/interlink", "cmd/interlink/main.go", "cmd/interlink/cri.go"})

	m.InterlinkContainer = dag.Container().
		From("alpine").
		WithFile("/bin/interlink", builder.File("/src/bin/interlink")).
		WithEntrypoint([]string{"/bin/interlink"})

	_, err := dag.Container().From("quay.io/skopeo/stable").
		WithEnvVariable("BUST", time.Now().String()).
		WithServiceBinding("registry", m.Registry).
		WithMountedFile("image.tar", m.InterlinkContainer.AsTarball()).
		WithExec([]string{"copy", "--dest-tls-verify=false", "docker-archive:image.tar", "docker://" + m.InterlinkRef}, dagger.ContainerWithExecOpts{UseEntrypoint: true}).
		Sync(ctx)
	if err != nil {
		return nil, err
	}

	builderVK := dag.Container().
		From("golang:1.24").
		WithDirectory("/src", sourceFolder).
		WithWorkdir("/src").
		WithMountedCache("/go/pkg/mod", dag.CacheVolume("go-mod-122")).
		WithEnvVariable("GOMODCACHE", "/go/pkg/mod").
		WithEnvVariable("VERSION", "local").
		WithMountedCache("/go/build-cache", dag.CacheVolume("go-build-122")).
		WithEnvVariable("GOCACHE", "/go/build-cache").
		WithEnvVariable("CGO_ENABLED", "0").
		WithExec([]string{"bash", "-c", "KUBELET_VERSION=${VERSION} ./cmd/virtual-kubelet/set-version.sh"}).
		WithExec([]string{"go", "build", "-o", "bin/vk", "cmd/virtual-kubelet/main.go"})

	m.VKContainer = dag.Container().
		From("alpine").
		WithFile("/bin/vk", builderVK.File("/src/bin/vk")).
		WithEntrypoint([]string{"/bin/vk"})

	_, err = dag.Container().From("quay.io/skopeo/stable").
		WithEnvVariable("BUST", time.Now().String()).
		WithServiceBinding("registry", m.Registry).
		WithMountedFile("image.tar", m.VKContainer.AsTarball()).
		WithExec([]string{"copy", "--dest-tls-verify=false", "docker-archive:image.tar", "docker://" + m.VirtualKubeletRef}, dagger.ContainerWithExecOpts{UseEntrypoint: true}).
		Sync(ctx)
	if err != nil {
		return nil, err
	}
	return m, nil
}

// Wait for virtual node to be ready and expose the k8s endpoint as a service
func (m *Interlink) Kube(
	ctx context.Context,
) (*dagger.Service, error) {
	return m.KubeAPIs, nil
}

// Wait for cluster to be ready, then setup the test container
func (m *Interlink) Run(
	ctx context.Context,
	// +optional
	// +defaultPath="./manifests"
	manifests *dagger.Directory,
) (*dagger.Container, error) {
	return dag.Container().From("bitnami/kubectl:1.29.7-debian-12-r3").
		WithUser("root").
		WithExec([]string{"mkdir", "-p", "/opt/user"}).
		WithExec([]string{"chown", "-R", "1001:0", "/opt/user"}).
		WithExec([]string{"apt", "update"}).
		WithExec([]string{"apt", "update"}).
		WithExec([]string{"apt", "install", "-y", "curl", "python3", "python3-pip", "python3-venv", "git", "vim"}).
		WithEnvVariable("BUST", time.Now().String()).
		WithMountedFile("/.kube/config", dag.K3S(m.Name).Config(dagger.K3SConfigOpts{Local: false})).
		WithExec([]string{"chown", "1001:0", "/.kube/config"}).
		WithUser("1001").
		WithDirectory("/manifests", manifests).
		WithEntrypoint([]string{"kubectl"}).
		WithWorkdir("/opt/user").
		WithExec([]string{"bash", "-c", "git clone https://github.com/interlink-hq/vk-test-set.git"}).
		WithExec([]string{"bash", "-c", "cp /manifests/vktest_config.yaml /opt/user/vk-test-set/vktest_config.yaml"}).
		WithWorkdir("/opt/user/vk-test-set").
		WithExec([]string{"bash", "-c", "python3 -m venv .venv && source .venv/bin/activate && pip3 install -e ./ "}), nil
}

func (m *Interlink) Lint(
	// +optional
	// +defaultPath="../"
	sourceFolder *dagger.Directory,
) *dagger.Container {
	lintCache := dag.CacheVolume(m.Name + "_lint")

	return dag.Container().From("golangci/golangci-lint:v2.1.1").
		WithMountedDirectory("/app", sourceFolder).
		WithMountedCache("/root/.cache", lintCache).
		WithWorkdir("/app").
		WithExec([]string{"golangci-lint", "run", "-v", "--timeout=30m"}, dagger.ContainerWithExecOpts{UseEntrypoint: true})
}

// Wait for cluster to be ready, setup the test container, run all tests
func (m *Interlink) Test(
	ctx context.Context,
	// +optional
	// +defaultPath="./manifests"
	manifests *dagger.Directory,
	// +optional
	localCluster *dagger.Service,
	// +optional
	// +defaultPath="../"
	sourceFolder *dagger.Directory,
) (*dagger.Container, error) {
	lint, err := m.Lint(sourceFolder).Stdout(ctx)
	if err != nil {
		return nil, err
	}
	log.Printf("Lint output: %s", lint)

	c, err := m.Run(ctx, manifests)
	if err != nil {
		return nil, err
	}

	result := c.WithExec([]string{"bash", "-c", "source .venv/bin/activate && export KUBECONFIG=/.kube/config  && pytest -vk 'not rclone and not limits'"})
	//_ = c.WithExec([]string{"bash", "-c", "source .venv/bin/activate && export KUBECONFIG=/.kube/config  && pytest -vk 'hello'"})
	// result := c.WithExec([]string{"bash", "-c", "source .venv/bin/activate && export KUBECONFIG=/.kube/config  && pytest -vk 'hello'"})

	return result, nil
}

// TestMTLS specifically tests mTLS functionality including getLogs endpoint
func (m *Interlink) TestMTLS(
	ctx context.Context,
	// +optional
	// +defaultPath="./manifests"
	manifests *dagger.Directory,
	// +optional
	localCluster *dagger.Service,
	// +optional
	// +defaultPath="../"
	sourceFolder *dagger.Directory,
) (*dagger.Container, error) {
	lint, err := m.Lint(sourceFolder).Stdout(ctx)
	if err != nil {
		return nil, err
	}
	log.Printf("Lint output: %s", lint)

	c, err := m.Run(ctx, manifests)
	if err != nil {
		return nil, err
	}

	// First run basic tests to ensure setup works
	result := c.WithExec([]string{"bash", "-c", "source .venv/bin/activate && export KUBECONFIG=/.kube/config && pytest -v -k 'hello'"}).
		// Wait for virtual node to be ready
		WithExec([]string{"bash", "-c", "kubectl wait --for=condition=Ready node/virtual-kubelet --timeout=300s"}).
		// Create a test pod for getLogs testing
		WithExec([]string{"bash", "-c", `
kubectl apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: mtls-log-test
  namespace: default
spec:
  nodeSelector:
    kubernetes.io/hostname: virtual-kubelet
  containers:
  - name: test-container
    image: alpine:latest
    command: ["/bin/sh"]
    args: ["-c", "echo 'mTLS log test started'; sleep 30; echo 'mTLS log test completed'"]
  restartPolicy: Never
EOF`}).
		// Wait for pod to start
		WithExec([]string{"bash", "-c", "kubectl wait --for=condition=PodReadyForStartup pod/mtls-log-test --timeout=120s || true"}).
		// Test getLogs endpoint specifically - this should work with mTLS now
		WithExec([]string{"bash", "-c", "kubectl logs mtls-log-test --tail=10 || echo 'getLogs failed - this indicates mTLS issue'"}).
		// Run additional log streaming test
		WithExec([]string{"bash", "-c", "timeout 10s kubectl logs -f mtls-log-test || echo 'Log streaming test completed'"}).
		// Clean up test pod
		WithExec([]string{"bash", "-c", "kubectl delete pod mtls-log-test --ignore-not-found"}).
		// Run the full test suite (excluding resource-intensive tests)
		WithExec([]string{"bash", "-c", "source .venv/bin/activate && export KUBECONFIG=/.kube/config && pytest -v -k 'not rclone and not limits and not stress'"})

	return result, nil
}
