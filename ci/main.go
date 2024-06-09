package main

import (
	"context"
	"fmt"
)

type Interlink struct {
	k8s *K8sInstance
	// TODO: services on NodePort?
	//virtualkubelet bool
	//interlink      bool
	//plugin         bool
	cleanup bool
}

func (i *Interlink) NewInterlink(
	ctx context.Context,
	manifests *Directory,
	// +optional
	kubeconfig *File,
	// +optional
	localCluster *Service,
) (*Container, error) {

	i.k8s = NewK8sInstance(ctx)
	if err := i.k8s.start(manifests, kubeconfig, localCluster); err != nil {
		return nil, err
	}

	err := i.k8s.waitForNodes()
	if err != nil {
		return nil, err
	}

	ns, _ := i.k8s.kubectl("create ns interlink")
	fmt.Println(ns)

	sa, err := i.k8s.kubectl("apply -f /manifests/service-account.yaml")
	if err != nil {
		return nil, err
	}
	fmt.Println(sa)

	vkConfig, err := i.k8s.kubectl("apply -f /manifests/virtual-kubelet-config.yaml")
	if err != nil {
		return nil, err
	}
	fmt.Println(vkConfig)

	vk, err := i.k8s.kubectl("apply -f /manifests/virtual-kubelet.yaml")
	if err != nil {
		return nil, err
	}
	fmt.Println(vk)

	if err := i.k8s.waitForVirtualKubelet(); err != nil {
		return nil, err
	}

	intConfig, err := i.k8s.kubectl("apply -f /manifests/interlink-config.yaml")
	if err != nil {
		return nil, err
	}
	fmt.Println(intConfig)
	// build interlink and push
	intL, err := i.k8s.kubectl("apply -f /manifests/interlink.yaml")
	if err != nil {
		return nil, err
	}
	fmt.Println(intL)

	if err := i.k8s.waitForInterlink(); err != nil {
		return nil, err
	}

	i.LoadPlugin(ctx)

	return i.k8s.container, nil
}

func (i *Interlink) LoadPlugin(ctx context.Context) (*Interlink, error) {
	pluginConfig, err := i.k8s.kubectl("apply -f /manifests/plugin-config.yaml")
	if err != nil {
		return nil, err
	}
	fmt.Println(pluginConfig)

	plugin, err := i.k8s.kubectl("apply -f /manifests/plugin.yaml")
	if err != nil {
		return nil, err
	}
	fmt.Println(plugin)

	if err := i.k8s.waitForPlugin(); err != nil {
		return nil, err
	}

	return i, nil
}

func (i *Interlink) Cleanup(ctx context.Context) error {

	cleanup, err := i.k8s.kubectl("delete -f /manifests/")
	if err != nil {
		return err
	}
	fmt.Println(cleanup)

	return nil
}

func (i *Interlink) Test(
	ctx context.Context,
	manifests *Directory,
	// +optional
	kubeconfig *File,
	// +optional
	localCluster *Service,
	// +optional
	// +default false
	cleanup bool,
) (*Container, error) {

	ctr, err := i.NewInterlink(ctx, manifests, kubeconfig, localCluster)
	if err != nil {
		return nil, err
	}

	result := ctr.
		WithWorkdir("/opt").
		WithExec([]string{"bash", "-c", "git clone https://github.com/interTwin-eu/vk-test-set.git"}, ContainerWithExecOpts{SkipEntrypoint: true}).
		WithExec([]string{"cp", "/manifests/vktest_config.yaml", "/opt/vk-test-set/vktest_config.yaml"}).
		WithWorkdir("/opt/vk-test-set").
		WithExec([]string{"bash", "-c", "python3 -m venv .venv && source .venv/bin/activate && pip3 install -e ./ "}, ContainerWithExecOpts{SkipEntrypoint: true}).
		WithExec([]string{"bash", "-c", "source .venv/bin/activate && export KUBECONFIG=/.kube/config && pytest -vk 'not virtual-kubelet-070-rclone-bind' || echo OPS "}, ContainerWithExecOpts{SkipEntrypoint: true})

	if i.cleanup {
		err = i.Cleanup(ctx)
		if err != nil {
			return nil, err
		}
	}

	return result, nil

}

func (i *Interlink) Run(
	ctx context.Context,
	manifests *Directory,
	// +optional
	kubeconfig *File,
	// +optional
	localCluster *Service,
	// +optional
	// +default false
	cleanup bool,
) (*Container, error) {

	ctr, err := i.NewInterlink(ctx, manifests, kubeconfig, localCluster)
	if err != nil {
		return nil, err
	}

	if i.cleanup {
		err = i.Cleanup(ctx)
		if err != nil {
			return nil, err
		}
	}

	return ctr, nil

}
