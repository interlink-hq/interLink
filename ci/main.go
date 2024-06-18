package main

import (
	"bytes"
	"context"
	"fmt"
	"html/template"

	"dagger/interlink/internal/dagger"
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

type Interlink struct {
	K8s               *K8sInstance
	VirtualKubeletRef string
	InterlinkRef      string
	Manifests         *Directory
	// TODO: services on NodePort?
	//virtualkubelet bool
	//interlink      bool
	//plugin         bool
	CleanupCluster bool
}

func (i *Interlink) BuildImages(
	ctx context.Context,
	// +optional
	// +default="ghcr.io/intertwin-eu/virtual-kubelet-inttw"
	virtualKubeletRef string,
	// +optional
	// +default="ghcr.io/intertwin-eu/interlink"
	interlinkRef string,
	// +optional
	// +default="ghcr.io/intertwin-eu/plugin-test"
	pluginRef string,
	sourceFolder *Directory,
) (*Interlink, error) {

	// TODO: get tag

	i.VirtualKubeletRef = virtualKubeletRef
	i.InterlinkRef = interlinkRef

	workspace := dag.Container().
		WithDirectory("/src", sourceFolder).
		WithWorkdir("/src").
		Directory("/src")

	_, err := dag.Container().
		Build(workspace, dagger.ContainerBuildOpts{
			Dockerfile: "docker/Dockerfile.vk",
		}).
		Publish(ctx, virtualKubeletRef)
	if err != nil {
		return nil, err
	}

	_, err = dag.Container().
		Build(workspace, dagger.ContainerBuildOpts{
			Dockerfile: "docker/Dockerfile.interlink",
		}).
		Publish(ctx, interlinkRef)
	if err != nil {
		return nil, err
	}

	return i, nil
}

func (i *Interlink) NewInterlink(
	ctx context.Context,
	manifests *Directory,
	// +optional
	kubeconfig *File,
	// +optional
	localCluster *Service,
) (*Interlink, error) {

	// create Kustomize patch for images to be used
	patch := patchSchema{}
	if i.InterlinkRef != "" && i.VirtualKubeletRef != "" {
		patch = patchSchema{
			InterLinkRef:      i.InterlinkRef,
			VirtualKubeletRef: i.VirtualKubeletRef,
		}
	} else {
		patch = patchSchema{
			InterLinkRef:      "ghcr.io/intertwin-eu/interlink",
			VirtualKubeletRef: "ghcr.io/intertwin-eu/virtual-kubelet-inttw",
		}
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

	// use the manifest folder defined in the chain and install components

	if manifests != nil {
		i.Manifests = manifests
	}

	fmt.Println(bufferVK.String())

	i.K8s = NewK8sInstance(ctx)
	if err := i.K8s.start(ctx, i.Manifests, bufferVK.String(), bufferIL.String(), kubeconfig, localCluster); err != nil {
		return nil, err
	}

	err = i.K8s.waitForNodes(ctx)
	if err != nil {
		return nil, err
	}

	ns, _ := i.K8s.kubectl(ctx, "create ns interlink")
	fmt.Println(ns)

	sa, err := i.K8s.kubectl(ctx, "apply -f /manifests/service-account.yaml")
	if err != nil {
		return nil, err
	}
	fmt.Println(sa)

	vkConfig, err := i.K8s.kubectl(ctx, "apply -k /manifests/")
	if err != nil {
		return nil, err
	}
	fmt.Println(vkConfig)

	return i, nil
}

func (i *Interlink) LoadPlugin(ctx context.Context) (*Interlink, error) {
	pluginConfig, err := i.K8s.kubectl(ctx, "apply -f /manifests/plugin-config.yaml")
	if err != nil {
		return nil, err
	}
	fmt.Println(pluginConfig)

	plugin, err := i.K8s.kubectl(ctx, "apply -f /manifests/plugin.yaml")
	if err != nil {
		return nil, err
	}
	fmt.Println(plugin)

	return i, nil
}

func (i *Interlink) Cleanup(ctx context.Context) error {

	cleanup, err := i.K8s.kubectl(ctx, "delete -f /manifests/")
	if err != nil {
		return err
	}
	fmt.Println(cleanup)

	return nil
}

func (i *Interlink) Test(
	ctx context.Context,
	// +optional
	manifests *Directory,
	// +optional
	kubeconfig *File,
	// +optional
	localCluster *Service,
	// +optional
	// +default false
	cleanup bool,
) (*Container, error) {

	if manifests != nil {
		i.Manifests = manifests
	}

	if err := i.K8s.waitForVirtualKubelet(ctx); err != nil {
		return nil, err
	}
	if err := i.K8s.waitForInterlink(ctx); err != nil {
		return nil, err
	}
	if err := i.K8s.waitForPlugin(ctx); err != nil {
		return nil, err
	}

	result := i.K8s.KContainer.
		WithWorkdir("/opt").
		WithExec([]string{"bash", "-c", "git clone https://github.com/interTwin-eu/vk-test-set.git"}, ContainerWithExecOpts{SkipEntrypoint: true}).
		WithExec([]string{"bash", "-c", "cp /manifests/vktest_config.yaml /opt/vk-test-set/vktest_config.yaml"}, ContainerWithExecOpts{SkipEntrypoint: true}).
		WithWorkdir("/opt/vk-test-set").
		WithExec([]string{"bash", "-c", "python3 -m venv .venv && source .venv/bin/activate && pip3 install -e ./ "}, ContainerWithExecOpts{SkipEntrypoint: true}).
		WithExec([]string{"bash", "-c", "source .venv/bin/activate && export KUBECONFIG=/.kube/config && pytest -vk 'not rclone' || echo OPS "}, ContainerWithExecOpts{SkipEntrypoint: true})

	if i.CleanupCluster {
		err := i.Cleanup(ctx)
		if err != nil {
			return nil, err
		}
	}

	return result, nil

}

func (i *Interlink) Run(
	ctx context.Context,
) (*Container, error) {

	if i.CleanupCluster {
		err := i.Cleanup(ctx)
		if err != nil {
			return nil, err
		}
	}

	return i.K8s.KContainer.
		WithWorkdir("/opt").
		WithExec([]string{"bash", "-c", "git clone https://github.com/interTwin-eu/vk-test-set.git"}, ContainerWithExecOpts{SkipEntrypoint: true}).
		WithExec([]string{"bash", "-c", "cp /manifests/vktest_config.yaml /opt/vk-test-set/vktest_config.yaml"}, ContainerWithExecOpts{SkipEntrypoint: true}).
		WithWorkdir("/opt/vk-test-set").
		WithExec([]string{"bash", "-c", "python3 -m venv .venv && source .venv/bin/activate && pip3 install -e ./ "}, ContainerWithExecOpts{SkipEntrypoint: true}), nil

}
