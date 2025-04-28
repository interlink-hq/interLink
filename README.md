# Welcome to the interLink Project

[![GitHub License](https://img.shields.io/github/license/interlink-hq/interlink)](https://img.shields.io/github/license/interlink-hq/interlink)
![GitHub Repo stars](https://img.shields.io/github/stars/interlink-hq/interlink)

![GitHub Release](https://img.shields.io/github/v/release/interlink-hq/interlink)
![Tested with Dagger](https://img.shields.io/badge/tested_with_dagger-v0.13.3-green)
[![Go Report Card](https://goreportcard.com/badge/github.com/interlink-hq/interlink)](https://goreportcard.com/report/github.com/interlink-hq/interlink)

[![Slack server](https://img.shields.io/badge/slack_server-8A2BE2?link=https%3A%2F%2Fjoin.slack.com%2Ft%2Fintertwin%2Fshared_invite%2Fzt-2cs67h9wz-2DFQ6EiSQGS1vlbbbJHctA)](https://join.slack.com/t/intertwin/shared_invite/zt-2cs67h9wz-2DFQ6EiSQGS1vlbbbJHctA)

![Interlink logo](./docs/static/img/interlink_logo.png)

interLink is a abstraction layer for the execution of any Kubernetes pod on any
remote resource capable of managing a Container execution lifecycle.

It facilitates the development of provider specific plugins for the
[Kubernetes Virtual Kubelet interface](https://virtual-kubelet.io/), so the
resource providers can leverage the power of virtual kubelet without a black
belt in kubernetes internals.

The project consists of two main components:

- **A Kubernetes Virtual Node:** based on the translating requests for a
  kubernetes pod execution into a remote call to the interLink API server.
- **The interLink API server:** a modular and pluggable REST server where you
  can create your own Container manager plugin (called sidecars), or use the
  existing ones: remote docker execution on a remote host, singularity Container
  on a remote SLURM batch system.

interLink is hosted by the
[Cloud Native Computing Foundation (CNCF)](https://cncf.io).

## Getting Started

For usage and development guides please refer to
[our site](https://interlink-hq.github.io/interLink/)

## Contributing

Our project welcomes contributions from any member of our community. To get
started contributing, please see our [Contributor Guide](./CONTRIBUTING.md).

## Providers

interLink is designed to ease the work required to include new remote providers.
It already targets a wide range of providers with container execution
capabilities, including but not limited to:

- **SLURM or HTCondor batch systems with Apptainer, Enroot, or Singularity**:
  These batch systems are widely used in high-performance computing environments
  to manage and schedule jobs. By integrating with container runtimes like
  Apptainer, Enroot, or Singularity, our solution can efficiently execute
  containerized tasks on these systems.
- **On-demand virtual machines with any container runtime**: This includes
  virtual machines that can be provisioned on-demand and support container
  runtimes such as Docker, Podman, or others. This flexibility allows for
  scalable and dynamic resource allocation based on workload requirements.
- **Remote Kubernetes clusters**: Our solution can extend the capabilities of
  existing Kubernetes clusters, enabling them to offload workloads to another
  remote cluster. This is particularly useful for distributing workloads across
  multiple clusters for better resource utilization and fault tolerance.
- **Lambda-like services**: These are serverless computing services that execute
  code in response to events and automatically manage the underlying compute
  resources. By targeting these services, our solution can leverage the
  scalability and efficiency of serverless architectures for containerized
  workloads. All of this, while exposing a bare Kubernetes API kind of
  orchestration.

## In Scope

- **K8s applications with tasks to be executed on HPC systems**: This target
  focuses on Kubernetes applications that require high-performance computing
  (HPC) resources for executing tasks (AI training and inference, ML algorithm
  optimizations etc). These tasks might involve complex computations,
  simulations, or data processing that benefit from the specialized hardware and
  optimized performance of HPC systems.

- **Remote "runner"-like application for heavy payload execution requiring
  GPUs**: This target is designed for applications that need to execute heavy
  computational payloads, particularly those requiring GPU resources. These
  applications can be run remotely, leveraging powerful GPU hardware to handle
  tasks such as machine learning model training, data analysis, or rendering.

- **Lambda-like functions calling on external resources**: This target involves
  running containers on demand with specific computing needs. Now these
  resources might also be outside of the Kubernetes cluster thanks to interLink
  functionality.

## Out of Scope

- **Long-running services**: Our solution is not designed for services that need
  to run continuously for extended periods. It is optimized for tasks that have
  a defined start and end, rather than persistent services exposing
  intra-cluster communication endpoints.
- **Kubernetes Federation**: We do not aim to support Kubernetes Federation,
  which involves managing multiple Kubernetes clusters as a single entity. Our
  focus is on enabling Kubernetes pods to execute on remote resources, not on
  federating all kind of resources on multiple clusters.

## Communications

- [![Slack server](https://img.shields.io/badge/slack_server-8A2BE2?link=https%3A%2F%2Fjoin.slack.com%2Ft%2Fintertwin%2Fshared_invite%2Fzt-2cs67h9wz-2DFQ6EiSQGS1vlbbbJHctA)](https://join.slack.com/t/intertwin/shared_invite/zt-2cs67h9wz-2DFQ6EiSQGS1vlbbbJHctA)

## Resources

[![Kubecon 2025](https://img.youtube.com/vi/bIxw1uK0QRQ/0.jpg)](https://www.youtube.com/watch?v=bIxw1uK0QRQ)
[![Kubecon AI days 2025](https://img.youtube.com/vi/vTg58Nd7_58/0.jpg)](https://www.youtube.com/watch?v=vTg58Nd7_58)
[![Kubecon AI days 2024](https://img.youtube.com/vi/M3uLQiekqo8/0.jpg)](https://www.youtube.com/watch?v=M3uLQiekqo8)

## License

This project is licensed under [Apache2](./LICENSE)

## Conduct

We follow the [CNCF Code of Conduct](./CODE_OF_CONDUCT.md)
