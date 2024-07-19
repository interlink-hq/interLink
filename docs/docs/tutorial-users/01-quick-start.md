---
sidebar_position: 1
toc_min_heading_level: 2
toc_max_heading_level: 5
---

# Quick-start: local environment

:::danger

__N.B.__ in the demo the oauth2 proxy authN/Z is disabled. DO NOT USE THIS IN PRODUCTION unless you know what you are doing.

:::

## Requirements

- [Docker](https://docs.docker.com/engine/install/)
- [Minikube](https://minikube.sigs.k8s.io/docs/start/) (kubernetes-version 1.27.1)
- Clone interlink repo:

```bash
git clone https://github.com/interTwin-eu/interLink.git 
```

## Connect a remote machine with Docker 

Move to example location:

```bash
cd interLink/example/interlink-docker
```

### Setup Kubernetes cluster

```bash
minikube start --kubernetes-version=1.27.1
```

### Deploy Interlink

#### Configure interLink

You need to provide the interLink IP address that should be reachable from the kubernetes pods. In case of this demo setup, that address __is the address of your machine__

```bash
export INTERLINK_IP_ADDRESS=XXX.XX.X.XXX

sed -i 's/InterlinkAddress:.*/InterlinkAddress: "http:\/\/'$INTERLINK_IP_ADDRESS'"/g'  vk/InterLinkConfig.yaml

sed -i 's/InterlinkAddres:.*/InterlinkAddress: "http:\/\/'$INTERLINK_IP_ADDRESS'"/g'  interlink/InterLinkConfig.yaml | sed -i 's/SidecarURL:.*/SidecarURL: "http:\/\/'$INTERLINK_IP_ADDRESS'"/g' interlink/InterLinkConfig.yaml

sed -i 's/InterlinkAddress:.*/InterlinkAddress: "http:\/\/'$INTERLINK_IP_ADDRESS'"/g'  interlink/sidecarConfig.yaml | sed -i 's/SidecarURL:.*/SidecarURL: "http:\/\/'$INTERLINK_IP_ADDRESS'"/g' interlink/sidecarConfig.yaml
```

#### Deploy virtualKubelet

Create the `vk` namespace:

```bash
kubectl create ns vk
```

Deploy the vk resources on the cluster with:

```bash
kubectl apply -n vk -k vk/
```

Check that both the pods and the node are in ready status

```bash
kubectl get pod -n vk

kubectl get node
```

#### Deploy interLink via docker compose

```bash
cd interlink

docker compose up -d
```

Check logs for both interLink APIs and SLURM sidecar:

```bash
docker logs interlink-interlink-1 

docker logs interlink-docker-sidecar-1
```

#### Deploy a sample application

```bash
kubectl apply -f ../test_pod.yaml 
```

Then observe the application running and eventually succeeding via:

```bash
kubectl get pod -n vk --watch
```

When finished, interrupt the watch with `Ctrl+C` and retrieve the logs with:

```bash
kubectl logs  -n vk test-pod-cfg-cowsay-dciangot
```

Also you can see with `docker ps` the container appearing on the `interlink-docker-sidecar-1` container with:

```bash
docker exec interlink-docker-sidecar-1  docker ps
```
