// A module to instantiate and tests interLink components
//
// Visit the interLink documentation for more info: https://intertwin-eu.github.io/interLink/docs/intro/
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

var (
	virtualKubeletPatch = `
kind: Deployment
metadata:
  name: virtual-kubelet
  namespace: interlink
spec:
  template:
    spec:
      containers:
      - name: inttw-vk
        image: "{{.VirtualKubeletRef}}"
`

//	#- name: interlink
//	#  image: "{{.InterLinkRef}}"
//
// `
)

type patchSchema struct {
	InterLinkRef      string
	VirtualKubeletRef string
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
	// +default="ghcr.io/intertwin-eu/interlink/virtual-kubelet-inttw:0.3.4"
	VirtualKubeletRef string,
	// +optional
	// +default="ghcr.io/intertwin-eu/interlink/interlink:0.3.4"
	InterlinkRef string,
	// +optional
	// +default="ghcr.io/intertwin-eu/interlink-sidecar-slurm/interlink-sidecar-slurm:0.3.8"
	pluginRef string,
) *Interlink {

	return &Interlink{
		Name:               name,
		VirtualKubeletRef:  VirtualKubeletRef,
		VKContainer:        dag.Container().From(VirtualKubeletRef),
		InterlinkRef:       InterlinkRef,
		InterlinkContainer: dag.Container().From(InterlinkRef),
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

	var err error
	if pluginEndpoint == nil {
		m.PluginContainer = dag.Container().From(m.PluginRef).
			WithFile("/etc/interlink/InterLinkConfig.yaml", pluginConfig).
			WithEnvVariable("SLURMCONFIGPATH", "/etc/interlink/InterLinkConfig.yaml").
			WithEnvVariable("SHARED_FS", "true").
			WithExposedPort(4000).
			WithExec([]string{}, dagger.ContainerWithExecOpts{UseEntrypoint: true, InsecureRootCapabilities: true})

		pluginEndpoint, err = m.PluginContainer.AsService().Start(ctx)
		if err != nil {
			return nil, err
		}
	}

	if interlinkEndpoint == nil {
		interlink := m.InterlinkContainer.
			WithFile("/etc/interlink/InterLinkConfig.yaml", interlinkConfig).
			WithServiceBinding("plugin", pluginEndpoint).
			WithEnvVariable("INTERLINKCONFIGPATH", "/etc/interlink/InterLinkConfig.yaml").
			WithExposedPort(3000).
			WithExec([]string{}, dagger.ContainerWithExecOpts{UseEntrypoint: true, InsecureRootCapabilities: true})

		interlinkEndpoint, err = interlink.AsService().Start(ctx)
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

	m.Manifests = manifests
	m.KubeAPIs = K3s.Server()
	m.KubeConfig = K3s.Config(dagger.K3SConfigOpts{Local: false})
	m.KubeConfigHost = K3s.Config(dagger.K3SConfigOpts{Local: true})

	// create Kustomize patch for images to be used
	patch := patchSchema{
		InterLinkRef:      m.InterlinkRef,
		VirtualKubeletRef: m.VirtualKubeletRef,
	}

	bufferIL := new(bytes.Buffer)

	virtualKubeletCompiler, err := template.New("vk").Parse(virtualKubeletPatch)
	if err != nil {
		return nil, err
	}

	bufferVK := new(bytes.Buffer)

	err = virtualKubeletCompiler.Execute(bufferVK, patch)
	if err != nil {
		return nil, err
	}

	fmt.Println(bufferVK.String())

	kubectl := dag.Container().From("bitnami/kubectl:1.29.7-debian-12-r3").
		WithServiceBinding("registry", m.Registry).
		WithServiceBinding("plugin", pluginEndpoint).
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
		WithNewFile("/manifests/virtual-kubelet-merge.yaml", bufferVK.String(), dagger.ContainerWithNewFileOpts{
			Permissions: 0o755,
		}).
		WithNewFile("/manifests/interlink-merge.yaml", bufferIL.String(), dagger.ContainerWithNewFileOpts{
			Permissions: 0o755,
		}).
		WithEntrypoint([]string{"kubectl"})

	m.Kubectl = kubectl

	ns, _ := kubectl.WithExec([]string{"create", "ns", "interlink"}, dagger.ContainerWithExecOpts{UseEntrypoint: true}).Stdout(ctx)
	fmt.Println(ns)

	sa, err := kubectl.WithExec([]string{"apply", "-f", "/manifests/service-account.yaml"}, dagger.ContainerWithExecOpts{UseEntrypoint: true}).Stdout(ctx)
	if err != nil {
		return nil, err
	}
	fmt.Println(sa)

	vkConfig, err := kubectl.WithExec([]string{"apply", "-k", "/manifests/"}, dagger.ContainerWithExecOpts{UseEntrypoint: true}).Stdout(ctx)
	if err != nil {
		return nil, err
	}
	fmt.Println(vkConfig)

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
		From("golang:1.22").
		WithDirectory("/src", sourceFolder).
		WithWorkdir("/src").
		WithMountedCache("/go/pkg/mod", dag.CacheVolume("go-mod-122")).
		WithEnvVariable("GOMODCACHE", "/go/pkg/mod").
		WithEnvVariable("VERSION", "local").
		WithMountedCache("/go/build-cache", dag.CacheVolume("go-build-122")).
		WithEnvVariable("GOCACHE", "/go/build-cache").
		WithEnvVariable("CGO_ENABLED", "0").
		WithExec([]string{"bash", "-c", "KUBELET_VERSION=${VERSION} ./cmd/virtual-kubelet/set-version.sh"}).
		WithExec([]string{"go", "build", "-o", "bin/interlink", "cmd/interlink/main.go"})

	m.InterlinkContainer = dag.Container().
		From("alpine").
		WithFile("/bin/interlink", builder.File("/src/bin/interlink")).
		WithEntrypoint([]string{"/bin/interlink"})

	_, err := dag.Container().From("quay.io/skopeo/stable").
		WithServiceBinding("registry", m.Registry).
		WithMountedFile("image.tar", m.InterlinkContainer.AsTarball()).
		WithExec([]string{"copy", "--dest-tls-verify=false", "docker-archive:image.tar", "docker://" + m.InterlinkRef}, dagger.ContainerWithExecOpts{UseEntrypoint: true}).
		Sync(ctx)
	if err != nil {
		return nil, err
	}

	builderVK := dag.Container().
		From("golang:1.22").
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
		WithMountedFile("/.kube/config", dag.K3S(m.Name).Config(dagger.K3SConfigOpts{Local: false})).
		WithExec([]string{"chown", "1001:0", "/.kube/config"}).
		WithUser("1001").
		WithDirectory("/manifests", manifests).
		WithEntrypoint([]string{"kubectl"}).
		WithWorkdir("/opt/user").
		WithExec([]string{"bash", "-c", "git clone https://github.com/interTwin-eu/vk-test-set.git"}).
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
