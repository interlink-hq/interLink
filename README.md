[![GitHub License](https://img.shields.io/github/license/interlink-hq/interlink)](https://img.shields.io/github/license/interlink-hq/interlink)
![GitHub Repo stars](https://img.shields.io/github/stars/interlink-hq/interlink)

![GitHub Release](https://img.shields.io/github/v/release/interlink-hq/interlink)
![Tested with Dagger](https://img.shields.io/badge/tested_with_dagger-v0.13.3-green)
[![Go Report Card](https://goreportcard.com/badge/github.com/interlink-hq/interlink)](https://goreportcard.com/report/github.com/interlink-hq/interlink)

[![Slack server](https://img.shields.io/badge/slack_server-8A2BE2?link=https%3A%2F%2Fjoin.slack.com%2Ft%2Fintertwin%2Fshared_invite%2Fzt-2cs67h9wz-2DFQ6EiSQGS1vlbbbJHctA)](https://join.slack.com/t/intertwin/shared_invite/zt-2cs67h9wz-2DFQ6EiSQGS1vlbbbJHctA)

![Interlink logo](./docs/static/img/interlink_logo.png)

## :information_source: Overview

### Introduction

InterLink aims to provide an abstraction for the execution of a Kubernetes pod
on any remote resource capable of managing a Container execution lifecycle. We
target to facilitate the development of provider specific plugins, so the
resource providers can leverage the power of virtual kubelet without a black
belt in kubernetes internals.

The project consists of two main components:

- **A Kubernetes Virtual Node:** based on the
  [VirtualKubelet](https://virtual-kubelet.io/) technology. Translating request
  for a kubernetes pod execution into a remote call to the interLink API server.
- **The interLink API server:** a modular and pluggable REST server where you
  can create your own Container manager plugin (called sidecars), or use the
  existing ones: remote docker execution on a remote host, singularity Container
  on a remote SLURM batch system.

The project got inspired by the [KNoC](https://github.com/CARV-ICS-FORTH/knoc)
and [Liqo](https://github.com/liqotech/liqo/tree/master) projects, enhancing
that with the implemention a generic API layer b/w the virtual kubelet component
and the provider logic for the container lifecycle management.

For usage and development guides please refer to
[our site](https://interlink-hq.github.io/interLink/)
