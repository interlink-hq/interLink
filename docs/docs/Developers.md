---
sidebar_position: 5
---

# Developers guide

Here you can find how to test a virtual kubelet implementation against the main
pod use cases we mean to support.

## K3s-based e2e tests (recommended)

The CI pipeline uses a K3s-based test environment that you can reproduce locally
without any special cloud account or token. The same three scripts used by the
GitHub Actions workflow (`k3s-test-setup.sh`, `k3s-test-run.sh`,
`k3s-test-cleanup.sh`) work on any Linux machine with Docker and Go installed.

### Requirements

- Linux host (bare metal or VM) with `sudo` access
- [Docker engine](https://docs.docker.com/engine/install/)
- Go 1.26+ (`go.mod` specifies the minimum version)
- `curl`, `git` (standard tools)

The setup script installs K3s automatically; you do **not** need `kubectl` or
`k3s` pre-installed.

### Run the full e2e suite locally

```bash
# Clone the repo and enter it
git clone https://github.com/interlink-hq/interLink.git
cd interLink

# (Optional) enable unprivileged user namespaces if your kernel restricts them
sudo sysctl -w kernel.unprivileged_userns_clone=1

# 1. Build images, start K3s, deploy interLink and the SLURM plugin
bash scripts/k3s-test-setup.sh

# 2. Run the pytest test suite against the live cluster
bash scripts/k3s-test-run.sh

# 3. Collect logs and tear down all resources
bash scripts/k3s-test-cleanup.sh
```

A convenience Makefile target runs all three steps in sequence:

```bash
make test-k3s
```

Individual targets are also available:

```bash
make test-k3s-setup    # setup only
make test-k3s-run      # run only (requires setup to have completed)
make test-k3s-cleanup  # cleanup only
```

### What the scripts do

| Script | Purpose |
|---|---|
| `k3s-test-setup.sh` | Installs K3s; builds `interlink-api` and `interlink-plugin` (SLURM) images; installs Traefik v3 CRDs and cert-manager; creates a CA ClusterIssuer; starts all containers; starts virtual-kubelet as a host process; approves kubelet CSRs; runs a Slurm/Apptainer smoke test; starts the rathole Docker Compose tunnel environment |
| `k3s-test-run.sh` | (1) Runs the `pytest` vk-test-set suite (pod lifecycle + port-forwarding infra) against the live cluster; (2) runs the standalone Docker tunnel tests in `test/portforward/` |
| `k3s-test-cleanup.sh` | Captures TLS resource state and rathole container logs; tears down Docker Compose; stops containers; kills virtual-kubelet; uninstalls K3s |

### Artefacts and logs

All logs and job output are written to a temporary directory created by the
setup script (printed as `TEST_DIR` at startup):

```
/tmp/interlink-test-XXXXXX/
  k3s-install.log               – K3s install output
  vk.log                        – virtual-kubelet stdout/stderr
  interlink-api.log             – interlink-api container logs (live-streamed)
  interlink-plugin.log          – interlink-plugin container logs (live-streamed)
  test-results.log              – pytest vk-test-set output
  portforward-test-results.log  – pytest Docker tunnel test output
  rathole-server-tcp.log        – rathole TCP server container logs
  rathole-server-ws.log         – rathole WebSocket server container logs
  plugin-jobs/                  – job directories from inside the plugin container
  slurm-logs/                   – Slurm daemon logs from inside the plugin container
```

### Environment variables

| Variable | Default | Description |
|---|---|---|
| `TEST_DIR` | auto-created under `/tmp` | Override the test directory path |
| `K3S_VERSION` | `v1.31.4+k3s1` | K3s version to install |

---

## Running individual test suites manually

Once `k3s-test-setup.sh` has completed (or you have an equivalent environment
running), you can iterate on individual test suites without re-running the full
setup.  All commands below assume you are at the repository root and the cluster
is reachable via `/etc/rancher/k3s/k3s.yaml`.

```bash
export KUBECONFIG=/etc/rancher/k3s/k3s.yaml
TEST_DIR=$(cat /tmp/interlink-test-dir.txt)   # set by setup script
```

### 1. VK pod integration tests (`test/vk-test-set/`)

These tests submit real pods to the virtual-kubelet node and validate pod
lifecycle, logs, volumes, probes, init containers, and projected volumes.

```bash
cd test/vk-test-set

# Create an isolated Python venv (avoids oauthlib conflicts on RHEL/Rocky 9)
python3 -m venv .venv
source .venv/bin/activate
pip install -q pytest kubernetes jinja2 pydantic requests pytest-timeout

# Run the full suite (excluding slow / optional test classes)
pytest -v -k "not rclone and not limits and not stress and not multi-init and not fail"

# Run a single template test
pytest -v vktestset/basic_test.py::test_manifest[virtual-kubelet-000-hello-world.yaml]

# Run the port-forwarding infrastructure tests (separate file)
pytest -v vktestset/port_forward_test.py

# Run one specific port-forwarding test
pytest -v vktestset/port_forward_test.py::test_rathole_annotation_set

deactivate
```

**What `port_forward_test.py` checks** — when a pod with `containerPort` is
scheduled on the virtual-kubelet node the VK must provision the full rathole
tunnel infrastructure.  The 10 tests verify:

| Test | Assertion |
|---|---|
| `test_port_forward_pod_scheduled` | Pod reaches `Running`/`Succeeded` |
| `test_rathole_annotation_set` | `interlink.eu/rathole-client-commands` annotation is set and non-empty |
| `test_shadow_namespace_exists` | `<namespace>-wstunnel` namespace is created |
| `test_rathole_deployment_exists` | Rathole server `Deployment` exists in the shadow namespace |
| `test_rathole_service_exists` | Rathole server `Service` exists in the shadow namespace |
| `test_rathole_server_certificate_ready` | cert-manager `Certificate` (server TLS) reaches `Ready=True` |
| `test_rathole_client_certificate_ready` | cert-manager `Certificate` (client TLS) reaches `Ready=True` |
| `test_tls_secrets_issued` | Both TLS `Secret`s contain `ca.crt`, `tls.crt`, `tls.key` |
| `test_traefik_ingressroutetcp_exists` | Traefik `IngressRouteTCP` is created for SNI routing |
| `test_annotation_contains_valid_toml` | Annotation encodes valid TOML: `[client]`, port `443`, TLS transport, `p8080` service |

**Troubleshooting**

```bash
# Check VK is running and Ready
kubectl get node virtual-kubelet

# Tail VK logs in real time
tail -f "${TEST_DIR}/vk.log"

# Check if interlink API can reach the plugin
curl http://localhost:3000/pinglink

# Inspect shadow namespace resources after a port-forward pod is created
kubectl get all,certificates -n interlink-wstunnel
```

> **Rocky/CentOS 9 note:** The system `oauthlib` package is missing
> `SIGNATURE_RSA`, which breaks the `kubernetes` Python client.  Always use a
> fresh `venv` (as shown above) rather than the system Python.

---

### 2. Docker tunnel unit tests (`test/portforward/`)

These tests are **independent of Kubernetes**.  They run against a local Docker
Compose topology that mimics the rathole tunnel between a remote HPC node and
the cluster side.  They can be run on any machine with Docker — no K3s required.

```bash
cd test/portforward

# Start the two-network Docker Compose environment
docker compose up -d

# Wait for tunnels to be ready (rathole clients retry automatically)
# You can watch tunnel handshake with:
docker compose logs -f rathole-client-tcp

# Install test dependencies
python3 -m venv .venv-pf
source .venv-pf/bin/activate
pip install pytest requests pytest-timeout "kubernetes>=28.0"

# Run all 37 tests
pytest -v

# Run only the TCP tunnel tests
pytest -v test_tunnel.py::TestTCPTunnel

# Run only the WebSocket tunnel tests
pytest -v test_tunnel.py::TestWebSocketTunnel

# Run the Kubernetes TLS resource tests (requires KUBECONFIG to be set)
# These verify cert-manager Certificates and Traefik IngressRouteTCP
export KUBECONFIG=/etc/rancher/k3s/k3s.yaml
pytest -v test_tls_k8s.py

deactivate

# Tear down when done
docker compose down -v
```

**Network topology**

```
[remote network – isolated "HPC" side]
  backend (nginx:alpine)
    port 80   → "Hello from remote backend"
    port 9090 → "Metrics from remote backend"

[cluster network – "Kubernetes" side]
  rathole-server-tcp   (TCP)       → host:18080, host:19090
  rathole-server-ws    (WebSocket) → host:18082

[bridge: remote ↔ cluster]
  rathole-client-tcp   forwards backend:80 and backend:9090 through TCP tunnel
  rathole-client-ws    forwards backend:80 through WebSocket tunnel
```

The backend has **no** ports exposed to the host, so the only way to reach it is
through a rathole tunnel — matching real interLink deployments where the remote
service is inside the HPC network.

**Environment variables for the Docker tests**

| Variable | Default | Description |
|---|---|---|
| `TCP_HTTP_URL` | `http://localhost:18080` | TCP tunnel primary endpoint |
| `TCP_METRICS_URL` | `http://localhost:19090` | TCP tunnel secondary port |
| `WS_HTTP_URL` | `http://localhost:18082` | WebSocket tunnel endpoint |
| `TUNNEL_WAIT_TIMEOUT` | `60` | Seconds to wait for tunnel readiness |

---

### 3. Quick health checks after setup

```bash
# Cluster and node status
kubectl get nodes
kubectl get pods -A

# VK connectivity to interlink API
curl http://localhost:3000/pinglink

# Shadow namespace should exist if a port-forwarding pod was scheduled before
kubectl get ns | grep wstunnel

# Check all cert-manager Certificates in shadow namespaces
kubectl get certificates -A | grep wstunnel

# Inspect a specific rathole tunnel resource set (replace with real pod name/ns)
kubectl get deployment,service,certificate,ingressroutetcp \
  -n interlink-wstunnel -l app=rathole
```

---

## Dagger-based tests (legacy)

> **Note:** The Dagger-based CI pipeline is disabled. The K3s-based workflow
> above is the authoritative test environment for all PRs.

The legacy Dagger setup is documented below for reference only.

### Requirements (Dagger)

- [Docker engine](https://docs.docker.com/engine/install/)
- [Dagger CLI v0.13.x](https://docs.dagger.io/install/)

### What's in the Dagger module (legacy)

- E2e integration tests: a reproducible test environment (selfcontained in
  Dagger runtime). Run the very same tests executed by github actions to
  validate any PR
- A development setup tool: optionally you can use your k8s cluster of choice to
  run and install different interlink components via this module.

:warning: by default the docker plugin is the one tested and to be referred to
for any change as first thing.

## Usage (Dagger, legacy)

The whole test suite is based on the application of k8s manifests inside a
folder that must be passed at runtime. In `./ci/manifests` of this repo you can
find the one executed by default by the github actions.

That means you can test your code **before** any commit, discovering in advance
if anything is breaking.

### Run e2e tests

The easiest way is to simply run `make test` from the root folder of interlink.
But if you need to debug or understand further the test utility or a plugin, you
should follow these instructions.

#### Edit manifests with your images

- `service-account.yaml` is the default set of permission needed by the
  virtualkubelet. Do not touch unless you know what you are doing.
- `virtual-kubelet-config.yaml` is the configuration mounted into the **virtual
  kubelet** component to determine its behaviour.
- `virtual-kubelet.yaml` is the one that you should touch if you are pointing to
  different interlink endpoints or if you want to change the **virtual kubelet**
  image to be tested.
- `interlink-config.yaml` is the configuration mounted into the **interlink
  API** component to determine its behaviour.
- `interlink.yaml` is the one that you should touch if you are pointing to
  different plugin endpoints or if you want to change the **interlink API**
  image to be tested.
- `plugin-config.yaml` is the configuration for the **interLink plugin**
  component that you MUST TO START MANUALLY on your host.
  - we do have solution to make it start inside dagger environment, but is not
    documented yet.

#### Start the local docker plugin service

For a simple demonstration, you can use the plugin that we actually use in are
Github Actions:

```bash
wget https://github.com/interlink-hq/interlink-docker-plugin/releases/download/0.0.24-no-gpu/docker-plugin_Linux_x86_64 -O docker-plugin \
  && chmod +x docker-plugin \
  && docker ps \
  && export INTERLINKCONFIGPATH=$PWD/ci/manifests/plugin-config.yaml \
  && ./docker-plugin
```

#### Run the tests

Then, in another terminal sessions you are ready to execute the e2e tests with
Dagger.

First of all, in `ci/manifests/vktest_config.yaml` you will find the pytest
configuration file. Please see the
[test documentation](https://github.com/interlink-hq/vk-test-set/tree/main) for
understanding how to tweak it.

The following instructions are thought for building docker images of the
virtual-kubelet and interlink api server components at runtime and published on
`virtual-kubelet-ref` and `interlink-ref` repositories (in this example it will
be dockerHUB repository of the dciangot user). It basically consists on a chain
of Dagger tasks for building core images (`build-images`), creating the
kubernetes environment configured with core components (`new-interlink`),
installing the plugin of choice indicated in the `manifest` folder
(`load-plugin`), and eventually the execution of the tests (`test`)

To run the default tests you can move to `ci` folder and execute the Dagger
pipeline with:

```bash
dagger call \
    --name my-tests \
  build-images \
  new-interlink \
    --plugin-endpoint tcp://localhost:4000 \
  test stdout
```

:warning: by default the docker plugin is the one tested and to be referred to
for any change as first thing.

In case of success the output should print something like the following:

```text
cachedir: .pytest_cache
rootdir: /opt/vk-test-set
configfile: pyproject.toml
collecting ... collected 12 items / 1 deselected / 11 selected

vktestset/basic_test.py::test_namespace_exists[default] PASSED           [  9%]
vktestset/basic_test.py::test_namespace_exists[kube-system] PASSED       [ 18%]
vktestset/basic_test.py::test_namespace_exists[interlink] PASSED         [ 27%]
vktestset/basic_test.py::test_node_exists[virtual-kubelet] PASSED        [ 36%]
vktestset/basic_test.py::test_manifest[virtual-kubelet-000-hello-world.yaml] PASSED [ 45%]
vktestset/basic_test.py::test_manifest[virtual-kubelet-010-simple-python.yaml] PASSED [ 54%]
vktestset/basic_test.py::test_manifest[virtual-kubelet-020-python-env.yaml] PASSED [ 63%]
vktestset/basic_test.py::test_manifest[virtual-kubelet-030-simple-shared-volume.yaml] PASSED [ 72%]
vktestset/basic_test.py::test_manifest[virtual-kubelet-040-config-volumes.yaml] PASSED [ 81%]
vktestset/basic_test.py::test_manifest[virtual-kubelet-050-limits.yaml] PASSED [ 90%]
vktestset/basic_test.py::test_manifest[virtual-kubelet-060-init-container.yaml] PASSED [100%]

====================== 11 passed, 1 deselected in 41.71s =======================
```

#### Debug with interactive session

In case something went wrong, you have the possibility to spawn a session inside
the final step of the pipeline to debug things:

```bash
dagger call \
    --name my-tests \
  build-images \
  new-interlink \
    --plugin-endpoint tcp://localhost:4000 \
  run terminal

```

with this command (after some minutes) then you should be able to access a bash
session doing the following commands:

```bash
bash
source .venv/bin/activate
export KUBECONFIG=/.kube/config

## check connectivity with k8s cluster
kubectl get pod -A

## re-run the tests
pytest -vk 'not rclone'
```

#### Debug from kubectl on your host

You can get the Kubernetes service running with:

```bash
dagger call \
    --name my-tests \
  build-images \
  new-interlink \
    --plugin-endpoint tcp://localhost:4000 \
  kube up
```

and then from another session, you can get the kubeconfig with:

```bash
dagger call \
    --name my-tests \
  config export --path ./kubeconfig.yaml
```

### Deploy on existing K8s cluster

TBD

<!--  -->
<!-- You might want to hijack the test machinery in order to have it instantiating the test environemnt on your own kubernetes cluster (e.g. to debug and develop plugins in a efficient way). We are introducing options for this purpose and it is expected to be extended even more in the future. -->
<!--  -->
<!-- If you have a kubernetes cluster **publically accessible**, you can pass your kubeconfig to the Dagger pipeline and use that instead of the internal one that is "one-shot" for the tests only. -->
<!--  -->
<!-- ```bash -->
<!-- ``` -->
<!--  -->
<!-- If you have a *local* cluster (e.g. via MiniKube), you need to forward the local port of the Kubernetes API server (look inside the kubeconfig file) inside the Dagger runtime with the following: -->
<!--  -->
<!-- ```bash -->
<!-- ``` -->

### Develop Virtual Kubelet code

:warning: Coming soon

### Develop Interlink API code

:warning: Coming soon

### Develop your plugin

:warning: Coming soon

## SSL Certificate Management

### CSR Integration for Virtual Kubelet

As of this version, Virtual Kubelet now supports proper SSL certificate management using Kubernetes Certificate Signing Requests (CSRs) instead of self-signed certificates. This resolves compatibility issues with `kubectl logs` and other Kubernetes clients.

#### Key Changes

- **CSR-based certificates**: Virtual Kubelet now requests certificates from the Kubernetes cluster CA using the standard `kubernetes.io/kubelet-serving` signer
- **Automatic fallback**: If CSR creation fails, the system falls back to self-signed certificates with a warning
- **Improved compatibility**: No longer requires `--insecure-skip-tls-verify-backend` flag for `kubectl logs`

#### Technical Details

The implementation uses:
- **Signer**: `kubernetes.io/kubelet-serving` (standard kubelet serving certificate signer)
- **Certificate store**: `/tmp/certs` directory with `virtual-kubelet` prefix
- **Subject**: `system:node:<node-name>` with `system:nodes` organization
- **IP SANs**: Node IP address for proper certificate validation

#### Testing Certificate Integration

To verify CSR-based certificate functionality:

1. **Check CSR creation**:
   ```bash
   kubectl get csr
   ```
   
2. **Test kubectl logs without insecure flag**:
   ```bash
   kubectl logs <pod-name-on-virtual-kubelet-node>
   ```

3. **Monitor Virtual Kubelet logs** for certificate retrieval messages:
   ```bash
   kubectl logs -n interlink virtual-kubelet-<node-name>
   ```

#### ⚠️ IMPORTANT: CSR Manual Approval Required

:exclamation: **CRITICAL**: CSRs (Certificate Signing Requests) must be manually approved by a cluster administrator, otherwise **log access will not work**. Without CSR approval, `kubectl logs` and other log-related operations will fail.

**Required steps for enabling log functionality:**

1. **Check for pending CSRs**:
   ```bash
   kubectl get csr
   ```

2. **Approve the CSR** (replace `csr-xxxxx` with the actual CSR name):
   ```bash
   kubectl certificate approve csr-xxxxx
   ```

3. **Verify logs are accessible**:
   ```bash
   kubectl logs <pod-name-on-virtual-kubelet-node>
   ```

#### Troubleshooting

- **CSR approval**: Ensure your cluster has automatic CSR approval configured or manually approve CSRs
- **RBAC permissions**: Virtual Kubelet needs permissions to create CSRs in the `certificates.k8s.io` API group
- **Fallback behavior**: Check logs for warnings about falling back to self-signed certificates

For clusters without proper CSR support, the system maintains backward compatibility by automatically using self-signed certificates with appropriate warnings.
