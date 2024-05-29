package main

import (
	"context"
	"fmt"
	"os"

	"dagger.io/dagger"
)

type Interlink struct {
	k8s                *K8sInstance
	pluginContainerRef string
	pluginConfigFile   *dagger.File
}

func (i *Interlink) Start() *dagger.Container {
	ctx := context.Background()

	// create Dagger client
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		panic(err)
	}
	defer client.Close()

	i.k8s = NewK8sInstance(ctx, client)
	if err = i.k8s.start(); err != nil {
		panic(err)
	}

	ns, err := i.k8s.kubectl("create ns interlink")
	if err != nil {
		panic(err)
	}
	fmt.Println(ns)

	sa, err := i.k8s.kubectl("apply -f /manifests/service-account.yaml")
	if err != nil {
		panic(err)
	}
	fmt.Println(sa)

	vkConfig, err := i.k8s.kubectl("apply -f /manifests/virtual-kubelet-config.yaml")
	if err != nil {
		panic(err)
	}
	fmt.Println(vkConfig)

	vk, err := i.k8s.kubectl("apply -f /manifests/virtual-kubelet.yaml")
	if err != nil {
		panic(err)
	}
	fmt.Println(vk)

	if err := i.k8s.waitForVirtualKubelet(); err != nil {
		panic(err)
	}

	intConfig, err := i.k8s.kubectl("apply -f /manifests/interlink-config.yaml")
	if err != nil {
		panic(err)
	}
	fmt.Println(intConfig)
	// build interlink and push
	intL, err := i.k8s.kubectl("apply -f /manifests/interlink.yaml")
	if err != nil {
		panic(err)
	}
	fmt.Println(intL)

	return i.k8s.container
}

func (i *Interlink) LoadPlugin() error {

	return nil
}

func (i *Interlink) Test(
	// +optional
	// +default="ghcr.io/intertwin-eu/interlink-docker-plugin/docker-plugin:0.0.8-no-gpu"
	pluginContainer string,
	// +optional
	// +default="./manifests/plugin-config.yaml"
	pluginConfig string) *dagger.Container {

	configTest := `
target_nodes: 
  - virtual-kubelet 

required_namespaces:
  - default
  - kube-system
  - interlink

values:
  namespace: interlink

  annotations: 
    slurm-job.vk.io/flags: "--job-name=test-pod-cfg -t 2800  --ntasks=8 --nodes=1 --mem-per-cpu=2000"

  tolerations:
    - key: virtual-node.interlink/no-schedule
      operator: Exists
      effect: NoSchedule
`

	setup_ctr := i.Start()

	i.pluginContainerRef = pluginContainer
	i.pluginConfigFile = i.k8s.client.Host().File(pluginConfig)

	i.LoadPlugin()

	return setup_ctr.
		WithExec([]string{"pip3", "install", "hatchling"}).
		WithWorkdir("/opt").
		WithExec([]string{"bash", "-c", "git clone https://github.com/landerlini/vk-test-set.git"}, dagger.ContainerWithExecOpts{SkipEntrypoint: true}).
		WithNewFile("/opt/vk-test-set/vktest_config.yaml", dagger.ContainerWithNewFileOpts{
			Contents:    configTest,
			Permissions: 0o655,
		}).
		WithWorkdir("/opt/vk-test-set").
		WithExec([]string{"bash", "-c", "python3 -m venv .venv && source .venv/bin/activate && pip3 install -e ./ "}, dagger.ContainerWithExecOpts{SkipEntrypoint: true}).
		WithExec([]string{"bash", "-c", "source .venv/bin/activate && export KUBECONFIG=/.kube/config && pytest -vx || echo OPS"}, dagger.ContainerWithExecOpts{SkipEntrypoint: true})

}
