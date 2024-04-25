package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dagger.io/dagger"
)

const registries = `mirrors:
	"registry:5432":
    endpoint:
      - "http://registry:5432"
`

// entrypoint to setup cgroup nesting since k3s only does it
// when running as PID 1. This doesn't happen in Dagger given that we're using
// our custom shim
const entrypoint = `#!/bin/sh

set -o errexit
set -o nounset

#########################################################################################################################################
# DISCLAIMER																																																														#
# Copied from https://github.com/moby/moby/blob/ed89041433a031cafc0a0f19cfe573c31688d377/hack/dind#L28-L37															#
# Permission granted by Akihiro Suda <akihiro.suda.cz@hco.ntt.co.jp> (https://github.com/k3d-io/k3d/issues/493#issuecomment-827405962)	#
# Moby License Apache 2.0: https://github.com/moby/moby/blob/ed89041433a031cafc0a0f19cfe573c31688d377/LICENSE														#
#########################################################################################################################################
if [ -f /sys/fs/cgroup/cgroup.controllers ]; then
  echo "[$(date -Iseconds)] [CgroupV2 Fix] Evacuating Root Cgroup ..."
	# move the processes from the root group to the /init group,
  # otherwise writing subtree_control fails with EBUSY.
  mkdir -p /sys/fs/cgroup/init
  busybox xargs -rn1 < /sys/fs/cgroup/cgroup.procs > /sys/fs/cgroup/init/cgroup.procs || :
  # enable controllers
  sed -e 's/ / +/g' -e 's/^/+/' <"/sys/fs/cgroup/cgroup.controllers" >"/sys/fs/cgroup/cgroup.subtree_control"
  echo "[$(date -Iseconds)] [CgroupV2 Fix] Done"
fi

exec "$@"
`

func NewK8sInstance(ctx context.Context, client *dagger.Client) *K8sInstance {
	return &K8sInstance{
		ctx:         ctx,
		client:      client,
		container:   nil,
		registry:    nil,
		configCache: client.CacheVolume("k3s_config"),
	}
}

type K8sInstance struct {
	ctx         context.Context
	client      *dagger.Client
	container   *dagger.Container
	registry    *dagger.Service
	configCache *dagger.CacheVolume
}

func (k *K8sInstance) start() error {

	registry := k.client.Host().Service(
		[]dagger.PortForward{
			{
				Backend:  5432,
				Frontend: 5432,
			},
		})

	// create k3s service container
	k3s := k.client.Pipeline("k3s init").Container().
		From("rancher/k3s").
		WithNewFile("/usr/bin/entrypoint.sh", dagger.ContainerWithNewFileOpts{
			Contents:    entrypoint,
			Permissions: 0o755,
		}).
		WithNewFile("/etc/rancher/k3s/registries.yaml", dagger.ContainerWithNewFileOpts{
			Contents: registries,
		}).
		WithEntrypoint([]string{"entrypoint.sh"}).
		WithServiceBinding("registry", registry).
		WithMountedCache("/etc/rancher/k3s", k.configCache).
		WithMountedTemp("/etc/lib/cni").
		WithMountedTemp("/var/lib/kubelet").
		WithMountedTemp("/var/lib/rancher/k3s").
		WithMountedTemp("/var/log").
		WithExec([]string{"sh", "-c", "k3s server --bind-address $(ip route | grep src | awk '{print $NF}') --disable traefik --disable metrics-server --egress-selector-mode=disabled"}, dagger.ContainerWithExecOpts{InsecureRootCapabilities: true}).
		WithExposedPort(6443)

	k.container = k.client.Container().
		From("bitnami/kubectl").
		WithMountedCache("/cache/k3s", k.configCache).
		WithMountedDirectory("/tests", k.client.Host().Directory("./tests")).
		WithServiceBinding("k3s", k3s.AsService()).
		WithEnvVariable("CACHE", time.Now().String()).
		WithUser("root").
		WithExec([]string{"cp", "/cache/k3s/k3s.yaml", "/.kube/config"}, dagger.ContainerWithExecOpts{SkipEntrypoint: true}).
		WithExec([]string{"chown", "1001:0", "/.kube/config"}, dagger.ContainerWithExecOpts{SkipEntrypoint: true}).
		WithUser("1001").
		WithEntrypoint([]string{"sh", "-c"})

	if err := k.waitForNodes(); err != nil {
		return fmt.Errorf("failed to start k8s: %v", err)
	}
	return nil
}

func (k *K8sInstance) kubectl(command string) (string, error) {
	return k.exec("kubectl", fmt.Sprintf("kubectl %v", command))
}

func (k *K8sInstance) exec(name, command string) (string, error) {
	return k.container.Pipeline(name).Pipeline(command).
		WithEnvVariable("CACHE", time.Now().String()).
		WithExec([]string{command}).
		Stdout(k.ctx)
}

func (k *K8sInstance) waitForNodes() (err error) {
	maxRetries := 5
	retryBackoff := 15 * time.Second
	for i := 0; i < maxRetries; i++ {
		time.Sleep(retryBackoff)
		kubectlGetNodes, err := k.kubectl("get nodes -o wide")
		if err != nil {
			fmt.Println(fmt.Errorf("could not fetch nodes: %v", err))
			continue
		}
		if strings.Contains(kubectlGetNodes, "Ready") {
			return nil
		}
		fmt.Println("waiting for k8s to start:", kubectlGetNodes)
	}
	return fmt.Errorf("k8s took too long to start")
}
