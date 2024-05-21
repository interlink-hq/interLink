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

sed -i 's/InterlinkURL:.*/InterlinkURL: "http:\/\/'$INTERLINK_IP_ADDRESS'"/g'  vk/InterLinkConfig.yaml

sed -i 's/InterlinkURL:.*/InterlinkURL: "http:\/\/'$INTERLINK_IP_ADDRESS'"/g'  interlink/InterLinkConfig.yaml | sed -i 's/SidecarURL:.*/SidecarURL: "http:\/\/'$INTERLINK_IP_ADDRESS'"/g' interlink/InterLinkConfig.yaml

sed -i 's/InterlinkURL:.*/InterlinkURL: "http:\/\/'$INTERLINK_IP_ADDRESS'"/g'  interlink/sidecarConfig.yaml | sed -i 's/SidecarURL:.*/SidecarURL: "http:\/\/'$INTERLINK_IP_ADDRESS'"/g' interlink/sidecarConfig.yaml
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

## Connect a SLURM batch system

Let's connect a cluster to a SLURM batch. Move to example location:

```bash
cd interLink/examples/interlink-slurm
```

### Setup Kubernetes cluster

:::danger

__N.B.__ in the demo the oauth2 proxy authN/Z is disabled. DO NOT USE THIS IN PRODUCTION unless you know what you are doing.

:::

### Bootstrap a minikube cluster

```bash
minikube start --kubernetes-version=1.26.10
```

Once finished you should check that everything went well with a simple `kubectl get node`. 

:::note

If you don't have `kubectl` installed on your machine, you can install it as describe in the [official documentation](https://kubernetes.io/docs/tasks/tools/)

:::

### Configure interLink

You need to provide the interLink IP address that should be reachable from the kubernetes pods.

:::note

In case of this demo setup, that address __is the address of your machine__

:::

```bash
export INTERLINK_IP_ADDRESS=XXX.XX.X.XXX

sed -i 's/InterlinkURL:.*/InterlinkURL: "http:\/\/'$INTERLINK_IP_ADDRESS'"/g'  vk/InterLinkConfig.yaml | sed -i 's/SidecarURL:.*/SidecarURL: "http:\/\/'$INTERLINK_IP_ADDRESS'"/g' vk/InterLinkConfig.yaml
```

### Deploy the interLink components

#### Deploy the interLink virtual node

Create a `vk` namespace:

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

#### Deploy interLink remote components

With the following commands you are going to deploy a docker compose that emulates a remote center managing resources via a SLURM batch system.

The following containers are going to be deployed:

- **interLink API server**: the API layer responsible of receiving requests from the kubernetes virtual node and forward a digested vertion to the interLink plugin
- **interLink SLURM plugin**: translates the information from the API server into a SLURM job
- **a SLURM local daemon**: a local instance of a SLURM dummy queue with [singularity/apptainer](https://apptainer.org/) available as container runtime.

```bash
cd interlink

docker compose up -d
```

Check logs for both interLink APIs and SLURM sidecar:

```bash
docker logs interlink-interlink-1 

docker logs interlink-docker-sidecar-1
```

### Deploy a sample application

Congratulation! Now it's all set up for the execution of your first pod on a virtual node!

What you have to do, is just explicitly allow a pod of yours in the following way:

```yaml title="./examples/interlink-slurm/test_pod.yaml"
apiVersion: v1
kind: Pod
metadata:
  name: test-pod-cowsay
  namespace: vk
  annotations:
    slurm-job.vk.io/flags: "--job-name=test-pod-cfg -t 2800  --ntasks=8 --nodes=1 --mem-per-cpu=2000"
spec:
  restartPolicy: Never
  containers:
  - image: docker://ghcr.io/grycap/cowsay 
    command: ["/bin/sh"]
    args: ["-c",  "\"touch /tmp/test.txt && sleep 60 && echo \\\"hello muu\\\" | /usr/games/cowsay \" " ]
    imagePullPolicy: Always
    name: cowsayo
  dnsPolicy: ClusterFirst
  // highlight-start
  nodeSelector:
    kubernetes.io/hostname: test-vk
  tolerations:
  - key: virtual-node.interlink/no-schedule
    operator: Exists
  // highlight-end
```

Then, you are good to go:

```bash
kubectl apply -f ../test_pod.yaml 
```

Now observe the application running and eventually succeeding via:

```bash
kubectl get pod -n vk --watch
```

When finished, interrupt the watch with `Ctrl+C` and retrieve the logs with:

```bash
kubectl logs  -n vk test-pod-cfg-cowsay-dciangot
```

Also you can see with `squeue --me` the jobs appearing on the `interlink-docker-sidecar-1` container with:

```bash
docker exec interlink-docker-sidecar-1 squeue --me
```

Or, if you need more debug, you can log into the sidecar and look for your POD_UID folder in `.local/interlink/jobs`:

```bash
docker exec -ti interlink-docker-sidecar-1 bash

ls -altrh .local/interlink/jobs
```
