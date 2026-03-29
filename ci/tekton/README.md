# interLink Tekton Pipeline

This directory contains a [Tekton Pipelines](https://tekton.dev/docs/pipelines/)
definition that replicates the end-to-end test workflow from [`ci/main.go`](../main.go)
(the Dagger-based CI pipeline).

## Overview

```
fetch-source  →  build-images  →  e2e-test
    (git-clone)     (Kaniko)        (DinD + k3d + pytest)
```

| Stage | Task | Description |
|-------|------|-------------|
| 1 | `git-clone` (Tekton catalog) | Clone the interLink repository with submodules |
| 2 | `interlink-build-images` | Build and push interLink API server and virtual-kubelet images via Kaniko |
| 3 | `interlink-e2e-test` | Spin up a k3d cluster in a Docker-in-Docker environment, deploy the full interLink stack, and run the pytest suite |

To test with already-published images (skipping the build stage), run the
`interlink-e2e-test` Task directly using a TaskRun — see
[Running only the e2e tests](#running-only-the-e2e-tests) below.

## Prerequisites

### Tekton

Tekton Pipelines **v0.50+** must be installed in the cluster:

```bash
kubectl apply -f \
  https://storage.googleapis.com/tekton-releases/pipeline/latest/release.yaml
```

### Tekton Hub — git-clone task

```bash
kubectl apply -f \
  https://api.hub.tekton.dev/v1/resource/tekton/task/git-clone/0.9/raw
```

### PersistentVolumeClaim

```bash
kubectl apply -f - <<EOF
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: interlink-source-pvc
spec:
  accessModes: [ReadWriteOnce]
  resources:
    requests:
      storage: 2Gi
EOF
```

### Registry credentials (required for `build-images` stage)

```bash
kubectl create secret docker-registry registry-credentials \
  --docker-server=ghcr.io \
  --docker-username=<github-user> \
  --docker-password=<github-pat>
```

## Installation

Apply the tasks and pipeline in order:

```bash
# 1. Custom tasks
kubectl apply -f ci/tekton/tasks/build-images.yaml
kubectl apply -f ci/tekton/tasks/e2e-test.yaml

# 2. Pipeline
kubectl apply -f ci/tekton/pipeline.yaml
```

## Running the full pipeline (build + test)

Edit `ci/tekton/pipeline-run.yaml` to set your registry and image references,
then:

```bash
kubectl create -f ci/tekton/pipeline-run.yaml
```

### Watch progress with the Tekton CLI

```bash
# Install tkn CLI: https://tekton.dev/docs/cli/
tkn pipelinerun logs -f -l app.kubernetes.io/part-of=interlink
```

## Running only the e2e tests

To run the e2e tests against already-published images (skipping the build
stage), create a TaskRun that calls `interlink-e2e-test` directly:

```bash
kubectl create -f - <<EOF
apiVersion: tekton.dev/v1
kind: TaskRun
metadata:
  generateName: interlink-e2e-test-run-
spec:
  taskRef:
    name: interlink-e2e-test
  params:
    - name: vk-image
      value: "ghcr.io/interlink-hq/interlink/virtual-kubelet-inttw:latest"
    - name: interlink-image
      value: "ghcr.io/interlink-hq/interlink/interlink:latest"
    - name: plugin-image
      value: "ghcr.io/interlink-hq/interlink-sidecar-slurm/interlink-sidecar-slurm:0.5.0"
  workspaces:
    - name: source
      persistentVolumeClaim:
        claimName: interlink-source-pvc
EOF
```

## File structure

```
ci/tekton/
├── README.md                  # This file
├── pipeline.yaml              # Pipeline resource (3 stages)
├── pipeline-run.yaml          # Example PipelineRun
└── tasks/
    ├── build-images.yaml      # Kaniko-based image build task
    └── e2e-test.yaml          # DinD / k3d e2e test task
```

## Mapping to ci/main.go

| Dagger function | Tekton equivalent |
|-----------------|-------------------|
| `BuildImages()` | `interlink-build-images` task — uses Kaniko instead of the Dagger Go SDK |
| `NewInterlink()` | Steps 7–18 in `interlink-e2e-test` (network, containers, k3d, VK helm install) |
| `Test()` → `Run()` + `pytest` | Steps 19–23 in `interlink-e2e-test` |

### Key differences from the Dagger pipeline

* **Image builds** use [Kaniko](https://github.com/GoogleContainerTools/kaniko)
  instead of the Dagger Go SDK so that no Docker daemon is needed in the build
  pod.
* **k3d** is used in place of the Dagger K3S module — it produces an equivalent
  single-node cluster inside the privileged e2e-test pod.
* **Docker-in-Docker** is used instead of Dagger service containers.  The
  plugin and interLink API server run as plain `docker run` processes on a
  shared Docker network (`interlink-net`), and the k3d cluster nodes are
  attached to the same network so the virtual-kubelet can reach the interLink
  API server by hostname.
* The **pytest filter** is identical to the one used in `Test()`:
  `not rclone and not limits and not stress and not multi-init and not fail`.

## Security considerations

The `interlink-e2e-test` task requires a **privileged** pod because:
1. `dockerd` must run inside the container (Docker-in-Docker).
2. `k3d` creates lightweight Kubernetes nodes as Docker containers.

Ensure that your cluster's admission policy allows privileged workloads for the
namespace where you run the pipeline (e.g. use a dedicated `interlink-ci`
namespace with appropriate PodSecurity labels).
