package main

import (
	"context"
	"fmt"
	"os"

	"dagger.io/dagger"
)

type Interlink struct {
	k8s *K8sInstance
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

	if err := i.k8s.waitForInterlink(); err != nil {
		panic(err)
	}

	return i.k8s.container
}

func (i *Interlink) LoadPlugin() error {
	pluginConfig, err := i.k8s.kubectl("apply -f /manifests/plugin-config.yaml")
	if err != nil {
		return err
	}
	fmt.Println(pluginConfig)

	plugin, err := i.k8s.kubectl("apply -f /manifests/plugin.yaml")
	if err != nil {
		return err
	}
	fmt.Println(plugin)

	if err := i.k8s.waitForPlugin(); err != nil {
		panic(err)
	}

	return nil
}

func (i *Interlink) Test() *dagger.Container {

	configTest := `
target_nodes: 
  - virtual-kubelet 

required_namespaces:
  - default
  - kube-system
  - interlink

timeout_multiplier: 10.
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

	err := i.LoadPlugin()
	if err != nil {
		panic(err)
	}

	return setup_ctr.
		WithWorkdir("/opt").
		WithExec([]string{"bash", "-c", "git clone -b init-test-fix-connection https://github.com/landerlini/vk-test-set.git"}, dagger.ContainerWithExecOpts{SkipEntrypoint: true}).
		WithNewFile("/opt/vk-test-set/vktest_config.yaml", dagger.ContainerWithNewFileOpts{
			Contents:    configTest,
			Permissions: 0o655,
		}).
		WithWorkdir("/opt/vk-test-set").
		WithExec([]string{"bash", "-c", "python3 -m venv .venv && source .venv/bin/activate && pip3 install -e ./ "}, dagger.ContainerWithExecOpts{SkipEntrypoint: true}).
		WithExec([]string{"bash", "-c", "source .venv/bin/activate && export KUBECONFIG=/.kube/config && pytest -vk 'not virtual-kubelet-070-rclone-bind'  "}, dagger.ContainerWithExecOpts{SkipEntrypoint: true})

}
