package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"dagger.io/dagger"
)

func main() {
	ctx := context.Background()

	// create Dagger client
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		panic(err)
	}
	defer client.Close()

	k8s := NewK8sInstance(ctx, client)
	if err = k8s.start(); err != nil {
		panic(err)
	}

	ns, err := k8s.kubectl("create ns interlink")
	if err != nil {
		panic(err)
	}
	fmt.Println(ns)

	sa, err := k8s.kubectl("apply -f /tests/manifests/service-account.yaml")
	if err != nil {
		panic(err)
	}
	fmt.Println(sa)

	vkConfig, err := k8s.kubectl("apply -f /tests/manifests/virtual-kubelet-config.yaml")
	if err != nil {
		panic(err)
	}
	fmt.Println(vkConfig)

	vk, err := k8s.kubectl("apply -f /tests/manifests/virtual-kubelet.yaml")
	if err != nil {
		panic(err)
	}
	fmt.Println(vk)

	if err := k8s.waitForVirtualKubelet(); err != nil {
		panic(err)
	}

	// build interlink and push
	// build mock and push
}

func (k *K8sInstance) waitForVirtualKubelet() (err error) {
	maxRetries := 5
	retryBackoff := 60 * time.Second
	for i := 0; i < maxRetries; i++ {
		time.Sleep(retryBackoff)
		kubectlGetPod, err := k.kubectl("get pod -n interlink -l nodeName=virtual-kubelet")
		if err != nil {
			fmt.Println(fmt.Errorf("could not fetch pod: %v", err))
			continue
		}
		if strings.Contains(kubectlGetPod, "2/2") {
			return nil
		}
		fmt.Println("waiting for k8s to start:", kubectlGetPod)
		describePod, err := k.kubectl("logs -n interlink -l nodeName=virtual-kubelet -c inttw-vk")
		if err != nil {
			fmt.Println(fmt.Errorf("could not fetch pod description: %v", err))
			continue
		}
		fmt.Println(describePod)

	}
	return fmt.Errorf("k8s took too long to start")
}
