// A module to instantiate and tests interLink components
//
// Visit the interLink documentation for more info: https://intertwin-eu.github.io/interLink/docs/intro/
//

package main

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"strings"
	"time"
)

var (
	interLinkPatch = `
kind: Deployment
metadata:
  name: interlink
  namespace: interlink
spec:
  template:
    spec:
      containers:
      - name: interlink
        image: "{{.InterLinkRef}}"

`
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
)

type patchSchema struct {
	InterLinkRef      string
	VirtualKubeletRef string
}

// Interlink struct for initialization and internal variables
type Interlink struct {
	Name              string
	Registry          *Service
	K3sService        *Service
	Manifests         *Directory
	VirtualKubeletRef string
	InterlinkRef      string
	// +private
	Kubectl *Container
}

// New initializes the Dagger module at each call
func New(name string,
	// +optional
	// +default="dciangot/docker-plugin:v1"
	pluginImage string,
	// +optional
	pluginEndpoint *Service,
	// +optional
	pluginConfig *File,
	manifests *Directory,
) *Interlink {

	regSvc := dag.Container().From("registry").
		WithExposedPort(5000).AsService()

	if pluginEndpoint == nil {
		plugin := dag.Container().From(pluginImage).
			WithFile("/etc/interlink/InterLinkConfig.yaml", pluginConfig).
			WithEnvVariable("INTERLINKCONFIGPATH", "/etc/interlink/InterLinkConfig.yaml").
			WithExec([]string{"bash", "-c", "dockerd --mtu 1450 & /sidecar/docker-sidecar & sleep infinity"}, ContainerWithExecOpts{InsecureRootCapabilities: true}).
			WithExposedPort(4000)

		pluginEndpoint = plugin.AsService()
	}

	K3s := dag.K3S(name).With(func(k *K3S) *K3S {
		return k.WithContainer(
			k.Container().
				WithEnvVariable("BUST", time.Now().String()).
				WithMountedDirectory("/manifests", manifests).
				WithExec([]string{"sh", "-c", `
cat <<EOF > /etc/rancher/k3s/registries.yaml
mirrors:
  "registry:5000":
    endpoint:
      - "http://registry:5000"
EOF`}, ContainerWithExecOpts{SkipEntrypoint: true}).
				WithServiceBinding("registry", regSvc).
				WithServiceBinding("plugin", pluginEndpoint),
		)
	})

	return &Interlink{
		Name:       name,
		Registry:   regSvc,
		K3sService: K3s.Server(),
		Manifests:  manifests,
	}
}

// Returns the kubeconfig file of the k3s cluster
func (m *Interlink) Config() *File {
	K3s := dag.K3S(m.Name)
	return K3s.Config(true)
}

// Utility to wait for nodes to be ready
func (m *Interlink) waitForNodes(ctx context.Context) (err error) {
	K3s := dag.K3S(m.Name)
	maxRetries := 10
	retryBackoff := 30 * time.Second
	for i := 0; i < maxRetries; i++ {
		kubectlGetNodes, err := K3s.Kubectl(ctx, "get nodes -o wide")
		if err != nil {
			fmt.Println(fmt.Errorf("could not fetch nodes: %v", err))
			continue
		}
		if strings.Contains(kubectlGetNodes, "Ready") {
			return nil
		}
		fmt.Println("waiting for k8s to start:", kubectlGetNodes)
		time.Sleep(retryBackoff)
	}
	return fmt.Errorf("k8s took too long to start")
}

// Utility that waits for virtual node to be ready
func (m *Interlink) waitForVirtualNodes(ctx context.Context) (err error) {
	K3s := dag.K3S(m.Name)
	maxRetries := 10
	retryBackoff := 30 * time.Second
	for i := 0; i < maxRetries; i++ {
		kubectlGetNodes, err := K3s.Kubectl(ctx, "get nodes -o wide virtual-kubelet")
		if err != nil {
			fmt.Println(fmt.Errorf("could not fetch nodes: %v", err))
			continue
		}
		if strings.Contains(kubectlGetNodes, "Ready") {
			time.Sleep(60 * time.Second)
			return nil
		}
		fmt.Println("waiting for k8s to start:", kubectlGetNodes)
		time.Sleep(retryBackoff)
	}
	return fmt.Errorf("k8s took too long to start")
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
	sourceFolder *Directory,
) (*Interlink, error) {

	// TODO: get tag

	m.VirtualKubeletRef = virtualKubeletRef
	m.InterlinkRef = interlinkRef

	workspace := dag.Container().
		WithDirectory("/src", sourceFolder).
		WithWorkdir("/src").
		Directory("/src")

	vkVersionSplits := strings.Split(virtualKubeletRef, ":")

	vkVersion := vkVersionSplits[len(vkVersionSplits)-1]
	if vkVersion == "" {
		return nil, fmt.Errorf("no tag specified on the image for VK")
	}

	_, err := dag.Container().From("quay.io/skopeo/stable").
		WithServiceBinding("registry", m.Registry).
		WithMountedFile("image.tar", dag.Container().
			Build(workspace, ContainerBuildOpts{
				Dockerfile: "docker/Dockerfile.vk",
				BuildArgs: []BuildArg{
					{"VERSION", vkVersion},
				},
			}).AsTarball()).
		WithExec([]string{"copy", "--dest-tls-verify=false", "docker-archive:image.tar", "docker://" + m.VirtualKubeletRef}).
		Sync(ctx)
	if err != nil {
		return nil, err
	}

	_, err = dag.Container().From("quay.io/skopeo/stable").
		WithServiceBinding("registry", m.Registry).
		WithMountedFile("image.tar", dag.Container().
			Build(workspace, ContainerBuildOpts{
				Dockerfile: "docker/Dockerfile.interlink",
				BuildArgs: []BuildArg{
					{"VERSION", vkVersion},
				},
			}).AsTarball()).
		WithExec([]string{"copy", "--dest-tls-verify=false", "docker-archive:image.tar", "docker://" + m.InterlinkRef}).
		Sync(ctx)
	if err != nil {
		return nil, err
	}
	return m, nil
}

// Setup interlink components:
// virtual kubelet and interlink API server
func (m *Interlink) NewInterlink(
	ctx context.Context,
	// +optional
	kubeconfig *File,
	// +optional
	localCluster *Service,
	// +optional
	pluginLocalService *Service,
) (*Interlink, error) {

	m.K3sService.Start(ctx)

	// create Kustomize patch for images to be used
	patch := patchSchema{
		InterLinkRef:      m.InterlinkRef,
		VirtualKubeletRef: m.VirtualKubeletRef,
	}

	interLinkCompiler, err := template.New("interlink").Parse(interLinkPatch)
	if err != nil {
		return nil, err
	}

	bufferIL := new(bytes.Buffer)

	err = interLinkCompiler.Execute(bufferIL, patch)
	if err != nil {
		return nil, err
	}

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

	K3s := dag.K3S(m.Name)
	kubectl := dag.Container().From("bitnami/kubectl").
		WithUser("root").
		WithExec([]string{"apt", "update"}, ContainerWithExecOpts{SkipEntrypoint: true}).
		WithExec([]string{"apt", "install", "-y", "curl", "python3", "python3-pip", "python3-venv", "git"}, ContainerWithExecOpts{SkipEntrypoint: true}).
		WithMountedFile("/.kube/config", K3s.Config(false)).
		WithExec([]string{"chown", "1001:0", "/.kube/config"}, ContainerWithExecOpts{SkipEntrypoint: true}).
		WithUser("1001").
		WithDirectory("/manifests", m.Manifests).
		WithNewFile("/manifests/virtual-kubelet-merge.yaml", ContainerWithNewFileOpts{
			Contents:    bufferVK.String(),
			Permissions: 0o755,
		}).
		WithNewFile("/manifests/interlink-merge.yaml", ContainerWithNewFileOpts{
			Contents:    bufferIL.String(),
			Permissions: 0o755,
		}).
		WithEntrypoint([]string{"kubectl"})

	m.Kubectl = kubectl

	ns, _ := kubectl.WithExec([]string{"create", "ns", "interlink"}).Stdout(ctx)
	fmt.Println(ns)

	sa, err := kubectl.WithExec([]string{"apply", "-f", "/manifests/service-account.yaml"}).Stdout(ctx)
	if err != nil {
		return nil, err
	}
	fmt.Println(sa)

	vkConfig, err := kubectl.WithExec([]string{"apply", "-k", "/manifests/"}).Stdout(ctx)
	if err != nil {
		return nil, err
	}
	fmt.Println(vkConfig)

	return m, nil
}

// Wait for virtual node to be ready and expose the k8s endpoint as a service
func (m *Interlink) Kube(
	ctx context.Context,
) (*Service, error) {

	if err := m.waitForVirtualNodes(ctx); err != nil {
		return nil, err
	}
	return m.K3sService, nil

}

// Wait for cluster to be ready, then setup the test container
func (m *Interlink) Run(
	ctx context.Context,
) (*Container, error) {

	if err := m.waitForVirtualNodes(ctx); err != nil {
		return nil, err
	}

	result := m.Kubectl.
		WithWorkdir("/opt").
		WithExec([]string{"bash", "-c", "git clone https://github.com/interTwin-eu/vk-test-set.git"}, ContainerWithExecOpts{SkipEntrypoint: true}).
		WithExec([]string{"bash", "-c", "cp /manifests/vktest_config.yaml /opt/vk-test-set/vktest_config.yaml"}, ContainerWithExecOpts{SkipEntrypoint: true}).
		WithWorkdir("/opt/vk-test-set").
		WithExec([]string{"bash", "-c", "python3 -m venv .venv && source .venv/bin/activate && pip3 install -e ./ "}, ContainerWithExecOpts{SkipEntrypoint: true}).
		WithExec([]string{"bash", "-c", "source .venv/bin/activate && export KUBECONFIG=/.kube/config"}, ContainerWithExecOpts{SkipEntrypoint: true})

	return result, nil

}

// Wait for cluster to be ready, setup the test container, run all tests
func (m *Interlink) Test(
	ctx context.Context,
	// +optional
	localCluster *Service,
	// +optional
	// +default false
	//cleanup bool,
) (*Container, error) {

	if err := m.waitForVirtualNodes(ctx); err != nil {
		return nil, err
	}

	result := m.Kubectl.
		WithWorkdir("/opt").
		WithExec([]string{"bash", "-c", "git clone https://github.com/interTwin-eu/vk-test-set.git"}, ContainerWithExecOpts{SkipEntrypoint: true}).
		WithExec([]string{"bash", "-c", "cp /manifests/vktest_config.yaml /opt/vk-test-set/vktest_config.yaml"}, ContainerWithExecOpts{SkipEntrypoint: true}).
		WithWorkdir("/opt/vk-test-set").
		WithExec([]string{"bash", "-c", "python3 -m venv .venv && source .venv/bin/activate && pip3 install -e ./ "}, ContainerWithExecOpts{SkipEntrypoint: true}).
		WithExec([]string{"bash", "-c", "source .venv/bin/activate && export KUBECONFIG=/.kube/config && pytest -vk 'not rclone'"}, ContainerWithExecOpts{SkipEntrypoint: true})

	return result, nil

}
