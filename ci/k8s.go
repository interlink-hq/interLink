package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"dagger.io/dagger"
)

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

func NewK8sInstance(ctx context.Context) *K8sInstance {
	return &K8sInstance{
		KContainer:      nil,
		Registry:        nil,
		ConfigCache:     dag.CacheVolume("k3s_config"),
		ContainersCache: dag.CacheVolume("k3s_containers"),
	}
}

type K8sInstance struct {
	KContainer      *Container
	K3s             *Container
	Registry        *Service
	ConfigCache     *CacheVolume
	ContainersCache *CacheVolume
}

func (k *K8sInstance) start(
	ctx context.Context,
	manifests *Directory,
	// +optional
	kubeconfig *File,
	// +optional
	localCluster *Service) error {

	// create a Dagger client
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stdout))
	if err != nil {
		panic(err)
	}
	defer client.Close()

	if kubeconfig == nil {
		// create k3s service container
		k.K3s = dag.Pipeline("k3s init").Container().
			From("rancher/k3s").
			WithNewFile("/usr/bin/entrypoint.sh", ContainerWithNewFileOpts{
				Contents:    entrypoint,
				Permissions: 0o755,
			}).
			WithEntrypoint([]string{"entrypoint.sh"}).
			WithMountedCache("/etc/rancher/k3s", k.ConfigCache).
			WithMountedTemp("/etc/lib/cni").
			WithMountedCache("/etc/lib/containers", k.ContainersCache).
			WithMountedTemp("/var/lib/kubelet").
			WithMountedTemp("/var/lib/rancher/k3s").
			WithMountedTemp("/var/log").
			WithExec([]string{"sh", "-c", "k3s server --bind-address $(ip route | grep src | awk '{print $NF}') --disable traefik --disable metrics-server --egress-selector-mode=disabled"}, ContainerWithExecOpts{InsecureRootCapabilities: true}).
			WithExposedPort(6443)

		k.KContainer = dag.Container().
			From("bitnami/kubectl").
			WithUser("root").
			WithExec([]string{"apt", "update"}, ContainerWithExecOpts{SkipEntrypoint: true}).
			WithExec([]string{"apt", "install", "-y", "curl", "python3", "python3-pip", "python3-venv", "git"}, ContainerWithExecOpts{SkipEntrypoint: true}).
			WithUser("1001").
			WithMountedCache("/cache/k3s", k.ConfigCache).
			WithMountedDirectory("/manifests", manifests).
			WithServiceBinding("k3s", k.K3s.AsService()).
			WithEnvVariable("CACHE", time.Now().String()).
			WithUser("root").
			WithExec([]string{"cp", "/cache/k3s/k3s.yaml", "/.kube/config"}, ContainerWithExecOpts{SkipEntrypoint: true}).
			WithExec([]string{"chown", "1001:0", "/.kube/config"}, ContainerWithExecOpts{SkipEntrypoint: true}).
			WithUser("1001").
			WithEntrypoint([]string{"sh", "-c"})

	} else if localCluster != nil {

		// k.KContainer, err = dag.Container().From("ubuntu").
		// 	WithServiceBinding("localhost", localCluster).
		// 	WithMountedDirectory("/manifests", manifests).
		// 	WithExec([]string{"apt", "update"}, ContainerWithExecOpts{SkipEntrypoint: true}).
		// 	WithExec([]string{"apt", "install", "-y", "curl"}, ContainerWithExecOpts{SkipEntrypoint: true}).
		// 	WithExec([]string{"curl", "-vvv", "localhost:59127"}).Sync(k.Ctx)
		// if err != nil {
		// 	return err
		// }

		fileName, _ := kubeconfig.Name(ctx)

		k.KContainer = dag.Container().
			From("bitnami/kubectl").
			WithUser("root").
			WithExec([]string{"apt", "update"}, ContainerWithExecOpts{SkipEntrypoint: true}).
			WithExec([]string{"apt", "install", "-y", "curl", "python3", "python3-pip", "python3-venv", "git"}, ContainerWithExecOpts{SkipEntrypoint: true}).
			WithUser("1001").
			WithMountedDirectory("/manifests", manifests).
			WithServiceBinding("minikube", localCluster).
			WithEnvVariable("CACHE", time.Now().String()).
			WithUser("root").
			WithFile(fmt.Sprintf("/src/%s", fileName), kubeconfig).
			WithExec([]string{"cp", fmt.Sprintf("/src/%s", fileName), "/.kube/config"}, ContainerWithExecOpts{SkipEntrypoint: true}).
			WithExec([]string{"chown", "1001:0", "/.kube/config"}, ContainerWithExecOpts{SkipEntrypoint: true}).
			WithUser("1001").
			WithEntrypoint([]string{"sh", "-c"})
	} else if localCluster == nil {

		fileName, _ := kubeconfig.Name(ctx)
		k.KContainer = dag.Container().
			From("bitnami/kubectl").
			WithUser("root").
			WithExec([]string{"apt", "update"}, ContainerWithExecOpts{SkipEntrypoint: true}).
			WithExec([]string{"apt", "install", "-y", "curl", "python3", "python3-pip", "python3-venv", "git"}, ContainerWithExecOpts{SkipEntrypoint: true}).
			WithUser("1001").
			WithMountedDirectory("/manifests", manifests).
			WithEnvVariable("CACHE", time.Now().String()).
			WithUser("root").
			WithFile(fmt.Sprintf("/src/%s", fileName), kubeconfig).
			WithExec([]string{"cp", fmt.Sprintf("/src/%s", fileName), "/.kube/config"}, ContainerWithExecOpts{SkipEntrypoint: true}).
			WithExec([]string{"chown", "1001:0", "/.kube/config"}, ContainerWithExecOpts{SkipEntrypoint: true}).
			WithUser("1001").
			WithEntrypoint([]string{"sh", "-c"})

	}
	return nil
}

func (k *K8sInstance) kubectl(ctx context.Context, command string) (string, error) {
	return k.exec(ctx, "kubectl", fmt.Sprintf("kubectl %v", command))
}

func (k *K8sInstance) exec(ctx context.Context, name, command string) (string, error) {
	return k.KContainer.Pipeline(name).Pipeline(command).
		WithEnvVariable("CACHE", time.Now().String()).
		WithExec([]string{command}).
		Stdout(ctx)
}

func (k *K8sInstance) waitForNodes(ctx context.Context) (err error) {
	maxRetries := 10
	retryBackoff := 30 * time.Second
	for i := 0; i < maxRetries; i++ {
		kubectlGetNodes, err := k.kubectl(ctx, "get nodes -o wide")
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

func (k *K8sInstance) waitForVirtualKubelet(ctx context.Context) (err error) {
	maxRetries := 10
	retryBackoff := 30 * time.Second
	for i := 0; i < maxRetries; i++ {
		time.Sleep(retryBackoff)
		kubectlGetPod, err := k.kubectl(ctx, "get pod -n interlink -l nodeName=virtual-kubelet")
		if err != nil {
			fmt.Println(fmt.Errorf("could not fetch pod: %v", err))
			continue
		}
		if strings.Contains(kubectlGetPod, "2/2") {
			return nil
		}
		fmt.Println("waiting for k8s to start:", kubectlGetPod)
		describePod, err := k.kubectl(ctx, "logs -n interlink -l nodeName=virtual-kubelet -c inttw-vk")
		if err != nil {
			fmt.Println(fmt.Errorf("could not fetch pod description: %v", err))
			continue
		}
		fmt.Println(describePod)

	}
	return fmt.Errorf("k8s took too long to start")
}

func (k *K8sInstance) waitForInterlink(ctx context.Context) (err error) {
	maxRetries := 10
	retryBackoff := 30 * time.Second
	for i := 0; i < maxRetries; i++ {
		time.Sleep(retryBackoff)
		kubectlGetPod, err := k.kubectl(ctx, "get pod -n interlink -l app=interlink")
		if err != nil {
			fmt.Println(fmt.Errorf("could not fetch pod: %v", err))
			continue
		}
		if strings.Contains(kubectlGetPod, "1/1") {
			return nil
		}
		fmt.Println("waiting for k8s to start:", kubectlGetPod)
		describePod, err := k.kubectl(ctx, "logs -n interlink -l app=interlink")
		if err != nil {
			fmt.Println(fmt.Errorf("could not fetch pod description: %v", err))
			continue
		}
		fmt.Println(describePod)

	}
	return fmt.Errorf("interlink took too long to start")
}

func (k *K8sInstance) waitForPlugin(ctx context.Context) (err error) {
	maxRetries := 10
	retryBackoff := 30 * time.Second
	for i := 0; i < maxRetries; i++ {
		time.Sleep(retryBackoff)
		kubectlGetPod, err := k.kubectl(ctx, "get pod -n interlink -l app=plugin")
		if err != nil {
			fmt.Println(fmt.Errorf("could not fetch pod: %v", err))
			continue
		}
		if strings.Contains(kubectlGetPod, "1/1") {
			return nil
		}
		fmt.Println("waiting for k8s to start:", kubectlGetPod)
		describePod, err := k.kubectl(ctx, "logs -n interlink -l app=plugin")
		if err != nil {
			fmt.Println(fmt.Errorf("could not fetch pod description: %v", err))
			continue
		}
		fmt.Println(describePod)

	}
	return fmt.Errorf("plugin took too long to start")
}
