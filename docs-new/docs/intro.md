---
sidebar_position: 1
---

# Introduction

:::warning

interLink is in early development phase, thus subject to breaking changes with no guarantee of backward compatibility.

:::

InterLink aims to provide an abstraction for the execution of a Kubernetes pod on any remote resource capable of managing a Container execution lifecycle.

The project consists of two main components:

- __A Kubernetes Virtual Node:__ based on the [VirtualKubelet](https://virtual-kubelet.io/) technology. Translating request for a kubernetes pod execution into a remote call to the interLink API server.
- __The interLink API server:__ a modular and pluggable REST server where you can create your own Container manager plugin (called sidecars), or use the existing ones: remote docker execution on a remote host, singularity Container on a remote SLURM batch system.

The project got inspired by the [KNoC](https://github.com/CARV-ICS-FORTH/knoc) and [Liqo](https://github.com/liqotech/liqo/tree/master) projects, enhancing that with the implemention a generic API layer b/w the virtual kubelet component and the provider logic for the container lifecycle management.

Let's discover [**interLink in less than 5 minutes**](./category/tutorial---end-users).

## What you'll need

You need only a machine with [Docker](https://docs.docker.com/engine/install/) engine and git CLI installed.
