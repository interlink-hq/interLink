# Integration Test Scripts

This directory contains helper scripts for running interLink integration tests with an ephemeral K3s cluster, providing an alternative to Dagger-based testing.

**Pattern**: These scripts follow the architecture from `example/debug.sh` but use `go run` instead of `dlv debug` for automated testing.

## Architecture

The test setup mirrors `example/debug.sh`:

```
┌─────────────────────────────────────────┐
│           K3s Cluster                   │
│  (Runs user pods scheduled to VK node)  │
└─────────────────────────────────────────┘
                ▲
                │ kubelet API (port 10250)
                │
┌───────────────┴─────────────────────────┐
│    Virtual Kubelet (Host Process)       │
│    go run ./cmd/virtual-kubelet/main.go │
└─────────────────────────────────────────┘
                │
                ▼ HTTP (port 3000)
┌─────────────────────────────────────────┐
│   interLink API (Docker Container)      │
│   Built from docker/Dockerfile.interlink│
└─────────────────────────────────────────┘
                │
                ▼ HTTP (port 4000)
┌─────────────────────────────────────────┐
│   SLURM Plugin (Docker Container)       │
│   Built from github.com/interlink-hq/   │
│   interlink-slurm-plugin                │
└─────────────────────────────────────────┘
```

## Scripts

### `k3s-test-setup.sh`
Sets up a complete ephemeral K3s cluster for integration testing.

**What it does:**
- Downloads and starts K3s (v1.31.4+k3s1)
- Builds Docker images from source:
  - interLink API (from `docker/Dockerfile.interlink`)
  - SLURM plugin (cloned from https://github.com/interlink-hq/interlink-slurm-plugin)
- Starts SLURM plugin as Docker container
- Starts interLink API as Docker container
- Starts Virtual Kubelet as host process (`go run`)
- Approves CSRs for kubectl logs support

**Usage:**
```bash
sudo ./scripts/k3s-test-setup.sh
```

**Requirements:**
- sudo access (for K3s installation)
- Docker (for building and running containers)
- Go 1.24+ (for running Virtual Kubelet)
- git (for cloning SLURM plugin repo)
- curl, kubectl

**Output:**
- Test directory: `/tmp/interlink-test-$$`
- Logs: `k3s.log`, `vk.log`
- Container logs: `docker logs interlink-api`, `docker logs interlink-plugin`
- PIDs: `k3s.pid`, `vk.pid`

### `k3s-test-run.sh`
Runs the vk-test-set integration test suite against the K3s cluster.

**What it does:**
- Verifies cluster is ready
- Approves pending CSRs
- Clones vk-test-set if needed
- Runs pytest integration tests
- Saves results to `test-results.log`

**Usage:**
```bash
./scripts/k3s-test-run.sh
```

**Requirements:**
- Must run after `k3s-test-setup.sh`
- Python 3 with pip

**Exit Codes:**
- 0: All tests passed
- Non-zero: Test failures

### `k3s-test-cleanup.sh`
Cleans up the ephemeral K3s cluster and all test resources.

**What it does:**
- Stops Virtual Kubelet host process
- Stops and removes Docker containers (interlink-api, interlink-plugin)
- Stops K3s cluster
- Removes test directory

**Usage:**
```bash
sudo ./scripts/k3s-test-cleanup.sh
```

**Requirements:**
- sudo access (for K3s cleanup)

### `local-test.sh`
Quick local integration test using your existing Kubernetes cluster.
Follows the `example/debug.sh` pattern with `go run`.

**What it does:**
- Validates kubectl connectivity
- Builds Docker images:
  - interLink API
  - SLURM plugin (from GitHub)
- Starts SLURM plugin container
- Starts interLink API container
- Provides instructions to start Virtual Kubelet with `go run`

**Usage:**
```bash
make test-local
# or
./scripts/local-test.sh
```

**Requirements:**
- Existing Kubernetes cluster with KUBECONFIG set
- Docker (for building and running containers)
- Go 1.24+ (for running Virtual Kubelet)
- git (for cloning SLURM plugin)

## Makefile Integration

Use these Makefile targets for convenience:

```bash
# Complete test cycle
make test-k3s

# Individual steps
make test-k3s-setup
make test-k3s-run
make test-k3s-cleanup

# Quick local test
make test-local
```

## Workflow

### Full Integration Test
```bash
# 1. Setup ephemeral cluster
sudo ./scripts/k3s-test-setup.sh

# 2. Run tests
./scripts/k3s-test-run.sh

# 3. Cleanup
sudo ./scripts/k3s-test-cleanup.sh
```

### Troubleshooting
If tests fail, check logs in the test directory:

```bash
TEST_DIR=$(cat /tmp/interlink-test-dir.txt)

# Component logs
cat $TEST_DIR/vk.log                    # Virtual Kubelet
docker logs interlink-api               # interLink API container
docker logs interlink-plugin            # SLURM plugin container
cat $TEST_DIR/k3s.log                   # K3s server

# Kubernetes status
export KUBECONFIG=/etc/rancher/k3s/k3s.yaml
kubectl get nodes                       # Check virtual-kubelet node
kubectl get pods -A                     # Check all pods
kubectl get events -A                   # Check events
kubectl get csr                         # Check CSRs
```

## GitHub Actions

The `.github/workflows/integration-test-k3s.yaml` workflow uses this same approach for CI/CD integration testing without Dagger dependencies.

## Differences from Dagger Tests

| Feature | Dagger Tests | K3s Scripts |
|---------|--------------|-------------|
| Plugin | Uses plugin ref | Builds SLURM plugin from GitHub |
| interLink API | Builds in Dagger | Builds Docker image locally |
| Virtual Kubelet | Runs in K8s | Runs as host process (`go run`) |
| Dependencies | Dagger CLI, Docker | K3s, Docker, Go |
| Isolation | Full containerization | Containers + host process |
| Speed | Slower (Dagger overhead) | Faster (direct execution) |
| Debugging | Interactive Dagger sessions | Direct log/process access |
| Cleanup | Automatic | Scripted |
| CI/CD | Dagger Cloud | Standard GitHub Actions |

## Test Configuration

Tests use configuration from `ci/manifests/`:
- `plugin-config.yaml`: SLURM plugin settings (generated at runtime)
- `interlink-config.yaml`: interLink API settings (generated at runtime)
- `virtual-kubelet-config.yaml`: Virtual Kubelet settings
- `vktest_config.yaml`: Test suite configuration

The scripts generate runtime configs in `/tmp/interlink-test-$$` based on these templates.

## Following example/debug.sh Pattern

These scripts mirror the development workflow from `example/debug.sh`:

| Component | debug.sh | Integration Tests |
|-----------|----------|-------------------|
| Plugin | SLURM plugin container | Same (built from source) |
| interLink API | Container from GHCR | Container (built from source) |
| Virtual Kubelet | `dlv debug` (interactive) | `go run` (automated) |
| Purpose | Development/debugging | Automated testing |
