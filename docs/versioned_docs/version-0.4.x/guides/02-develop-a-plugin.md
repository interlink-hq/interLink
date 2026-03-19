---
sidebar_position: 2
---

# Develop an interLink plugin

Learn how to develop your interLink plugin to manage containers on your remote
host.

We are going to follow up
[the setup of an interlink node](../cookbook/1-edge.mdx) with the last piece of
the puzzle:

- setup of a python SDK
- demoing the fundamentals development of a plugin executing containers locally
  through the host docker daemon

:::warning

The python SDK also produce an openAPI spec through FastAPI, therefore you can
use any language you want as long as the API spec is satisfied.

:::

## Setup the python SDK

### Requirements

- The tutorial is done on a Ubuntu VM, but there are not hard requirements
  around that
- Python>=3.10 and pip (`sudo apt install -y python3-pip`)
- Any python IDE will work and it is strongly suggested to use one :)
- A [docker engine running](https://docs.docker.com/engine/install/)

### Install the SDK

Look for the latest release on
[the release page](https://github.com/interlink-hq/interLink/releases) and set
the environment variable `VERSION` to it. Then you are ready to install the
python SDK with:

```bash
#export VERSION=X.X.X
#pip install "uvicorn[standard]" "git+https://github.com/interlink-hq/interlink-plugin-sdk@${VERSION}"

# Or download the latest one with
pip install "uvicorn[standard]" "git+https://github.com/interlink-hq/interlink-plugin-sdk"

```

In the next section we are going to leverage the provider class of SDK to create
our own plugin.
