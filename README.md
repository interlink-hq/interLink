[![GitHub License](https://img.shields.io/github/license/intertwin-eu/interlink)](https://img.shields.io/github/license/intertwin-eu/interlink)
![GitHub Repo stars](https://img.shields.io/github/stars/intertwin-eu/interlink)

![GitHub Release](https://img.shields.io/github/v/release/intertwin-eu/interlink)
![Tested with Dagger](https://img.shields.io/badge/tested_with_dagger-v0.13.3-green)
[![Go Report Card](https://goreportcard.com/badge/github.com/intertwin-eu/interlink)](https://goreportcard.com/report/github.com/intertwin-eu/interlink)

[![Slack server](https://img.shields.io/badge/slack_server-8A2BE2?link=https%3A%2F%2Fjoin.slack.com%2Ft%2Fintertwin%2Fshared_invite%2Fzt-2cs67h9wz-2DFQ6EiSQGS1vlbbbJHctA)](https://join.slack.com/t/intertwin/shared_invite/zt-2cs67h9wz-2DFQ6EiSQGS1vlbbbJHctA)

![Interlink logo](./docs/static/img/interlink_logo.png)

## :information_source: Overview

### Introduction
InterLink aims to provide an abstraction for the execution of a Kubernetes pod on any remote resource capable of managing a Container execution lifecycle.
We target to facilitate the development of provider specific plugins, so the resource providers can leverage the power of virtual kubelet without a black belt in kubernetes internals.

The project consists of two main components:

- __A Kubernetes Virtual Node:__ based on the [VirtualKubelet](https://virtual-kubelet.io/) technology. Translating request for a kubernetes pod execution into a remote call to the interLink API server.
- __The interLink API server:__ a modular and pluggable REST server where you can create your own Container manager plugin (called sidecars), or use the existing ones: remote docker execution on a remote host, singularity Container on a remote SLURM batch system.

The project got inspired by the [KNoC](https://github.com/CARV-ICS-FORTH/knoc) and [Liqo](https://github.com/liqotech/liqo/tree/master) projects, enhancing that with the implemention a generic API layer b/w the virtual kubelet component and the provider logic for the container lifecycle management.

For usage and development guides please refer to [our site](https://intertwin-eu.github.io/interLink/)

