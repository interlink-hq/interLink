---
sidebar_position: 5
---

# Developers guide

Here you can find how to test a virtual kubelet implementation against the main
pod use cases we mean to support.

## Requirements

- [Docker engine](https://docs.docker.com/engine/install/)
- [Dagger CLI v0.13.x](https://docs.dagger.io/install/)

## What's in the Dagger module

- E2e integration tests: a reproducible test environment (selfcontained in
  Dagger runtime). Run the very same tests executed by github actions to
  validate any PR
- A development setup tool: optionally you can use your k8s cluster of choice to
  run and install different interlink components via this module.

:warning: by default the docker plugin is the one tested and to be referred to
for any change as first thing.

## Usage

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

#### Troubleshooting

- **CSR approval**: Ensure your cluster has automatic CSR approval configured or manually approve CSRs
- **RBAC permissions**: Virtual Kubelet needs permissions to create CSRs in the `certificates.k8s.io` API group
- **Fallback behavior**: Check logs for warnings about falling back to self-signed certificates

For clusters without proper CSR support, the system maintains backward compatibility by automatically using self-signed certificates with appropriate warnings.
