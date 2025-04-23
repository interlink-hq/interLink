---
sidebar_position: 2
---

# Develop an interLink plugin

Learn how to develop your interLink plugin to manage containers on your remote
host.

We are going to follow up
[the setup of an interlink node](./01-deploy-interlink.mdx) with the last piece
of the puzzle:

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

### Plugin provider

The
[provider class](https://github.com/interlink-hq/interLink/blob/main/example/interlink/provider.py)
is a FastAPI interface that aims to isolate the developers from all the API
provisioning boiler plate.

In fact, we are going to need only the creation of a derived class implementing
the
[interLink core methods](https://github.com/interlink-hq/interLink/blob/main/example/interlink/provider.py#L14-L24),
and making use of in
[request and response API specification](https://github.com/interlink-hq/interLink/blob/main/example/interlink/spec.py)
to create our own container lifecycle management plugin.

:::warning

Be aware that interLink is a development phase, therefore there is no long term
guarantee for the API spec to be stable. Regardless, we are trying hard to keep
things as easy and stable as possible for a nice community experience.

:::

## Implementing the provider methods

Let's start installing the Docker python bindings, since in this example we want
to:

- convert a
  [Pod](https://github.com/interlink-hq/interLink/blob/main/example/interlink/spec.py#L65)
  into a `docker run` execution
- convert a
  [Delete or State pod request](https://github.com/interlink-hq/interLink/blob/main/example/interlink/spec.py#L47)
  into `docker rm` and `docker ps`,
- convert a
  [Log request](https://github.com/interlink-hq/interLink/blob/main/example/interlink/spec.py#L103)
  into a `docker logs`

```bash
pip install docker
```

Now we can start creating our `docker-plugin.py` script initializing the SDK
provider class:

```python
import interlink

from fastapi import FastAPI, HTTPException
from typing import List
import docker
import re
import os
import pprint
from datetime import datetime

# Initialize the docker client
dockerCLI = docker.DockerClient()

# Initialize FastAPI app
app = FastAPI()

# Define my custom interLink provider
class MyProvider(interlink.provider.Provider):
    def __init__(
        self,
        DOCKER
    ):
        super().__init__(DOCKER)

        # Recover container ID to pod UID map for the already running containers
        self.CONTAINER_POD_MAP = {}
        statuses = self.DOCKER.api.containers(all=True)
        for status in statuses:
            name = status["Names"][0]
            if len(name.split("-")) > 1:
                uid = "-".join(name.split("-")[-5:])
                self.CONTAINER_POD_MAP.update({uid: [status["Id"]]})
        print(self.CONTAINER_POD_MAP)


# Please Take my provider and handle the interLink REST layer for me
ProviderDocker = MyProvider(dockerCLI)

@app.post("/create")
async def create_pod(pods: List[interlink.Pod]) -> str:
    return ProviderDocker.create_pod(pods)

@app.post("/delete")
async def delete_pod(pod: interlink.PodRequest) -> str:
    return ProviderDocker.delete_pod(pod)

@app.get("/status")
async def status_pod(pods: List[interlink.PodRequest]) -> List[interlink.PodStatus]:
    return ProviderDocker.get_status(pods)

@app.get("/getLogs")
async def get_logs(req: interlink.LogRequest) -> bytes:
    return ProviderDocker.get_logs(req)
```

This empty provider is already good to be started:

```bash
uvicorn docker-plugin:app --reload --host 0.0.0.0 --port 4000 --log-level=debug
```

At this stage, it will respond with "NOT IMPLEMENTED" errors for all the
requests. The initialization part will only take care of importing the docker
client and store or recover the status of the running containers.

It's time to put our hands on the actual container management workflow.

### The Create request

:::warning

For simplicity, we are going to work just with the first container of the pod.
Feel free to generalize this for a many-containers-pod.

:::

Let's implement the `Create` method of the `MyProvider` class:

```python
    def Create(self, pod: interlink.Pod) -> None:
        # Get the first container of the request
        container = pod.pod.spec.containers[0]

        # Build the docker container execution command
        try:
            cmds = " ".join(container.command)
            args = " ".join(container.args)
            dockerContainer = self.DOCKER.containers.run(
                f"{container.image}:{container.tag}",
                f"{cmds} {args}",
                name=f"{container.name}-{pod.pod.metadata.uid}",
                detach=True,
            )
            docker_run_id = dockerContainer.id
        except Exception as ex:
            raise HTTPException(status_code=500, detail=ex)

        # Store the container ID to pod UID map information
        self.CONTAINER_POD_MAP.update({pod.pod.metadata.uid: [docker_run_id]})
```

As you can see, here we are getting the basic information we needed to launch a
container with Docker, updating the status cache dictionary `CONTAINER_POD_MAP`
afterwards.

For fields available in `interlink.Pod` request please refer to the
[spec file](https://github.com/interlink-hq/interLink/blob/main/example/interlink/spec.py).

### The Delete request

At this point there is nothing new anymore. The delete request should indeed
take care of the deletion of the container for the pod in the request:

```python
    def Delete(self, pod: interlink.PodRequest) -> None:
        try:
            print(f"docker rm -f {self.CONTAINER_POD_MAP[pod.metadata.uid][0]}")
            container = self.DOCKER.containers.get(self.CONTAINER_POD_MAP[pod.metadata.uid][0])
            container.remove(force=True)
            self.CONTAINER_POD_MAP.pop(pod.metadata.uid)
        except:
            raise HTTPException(status_code=404, detail="No containers found for UUID")
        return
```

### The Status request

The status request takes care of the returing a proper
[PodStatus](https://github.com/interlink-hq/interLink/blob/main/example/interlink/spec.py#L89C1-L93C38)
response for the pod in the request:

```python
    def Status(self,  pod: interlink.PodRequest) -> interlink.PodStatus:
        # Collect the container status
        try:
            container = self.DOCKER.containers.get(self.CONTAINER_POD_MAP[pod.metadata.uid][0])
            status = container.status
        except:
            raise HTTPException(status_code=404, detail="No containers found for UUID")

        match status:
            # If running: get the start time and return a running pod state
            case "running":
                try:
                    statuses = self.DOCKER.api.containers(filters={"status":"running", "id": container.id})
                    # Convert data to the correct format
                    startedAt = statuses[0]["Created"]
                    startedAt = datetime.utcfromtimestamp(startedAt).strftime('%Y-%m-%dT%H:%M:%SZ')
                except Exception as ex:
                    raise HTTPException(status_code=500, detail=ex)

                return interlink.PodStatus(
                        name=pod.metadata.name,
                        UID=pod.metadata.uid,
                        namespace=pod.metadata.namespace,
                        containers=[
                            interlink.ContainerStatus(
                                name=pod.spec.containers[0].name,
                                state=interlink.ContainerStates(
                                    running=interlink.StateRunning(startedAt=startedAt),
                                    waiting=None,
                                    terminated=None,
                                )
                            )
                        ]
                    )
            # If exited, collect the exitcode and the reason, then file a valid PodStatus with those info
            case "exited":
                try:
                    statuses = self.DOCKER.api.containers(filters={"status":"exited", "id": container.id})
                    reason = statuses[0]["Status"]
                    pattern = re.compile(r'Exited \((.*?)\)')

                    exitCode = -1
                    for match in re.findall(pattern, reason):
                        exitCode = int(match)
                except Exception as ex:
                    raise HTTPException(status_code=500, detail=ex)

                return interlink.PodStatus(
                        name=pod.metadata.name,
                        UID=pod.metadata.uid,
                        namespace=pod.metadata.namespace,
                        containers=[
                            interlink.ContainerStatus(
                                name=pod.spec.containers[0].name,
                                state=interlink.ContainerStates(
                                    running=None,
                                    waiting=None,
                                    terminated=interlink.StateTerminated(
                                        reason=reason,
                                        exitCode=exitCode
                                    ),
                                )
                            )
                        ]
                    )

            # If none of the above are true, the container ended with 0 exit code. Set the status to completed
            case _:
                return interlink.PodStatus(
                        name=pod.metadata.name,
                        UID=pod.metadata.uid,
                        namespace=pod.metadata.namespace,
                        containers=[
                            interlink.ContainerStatus(
                                name=pod.spec.containers[0].name,
                                state=interlink.ContainerStates(
                                    running=None,
                                    waiting=None,
                                    terminated=interlink.StateTerminated(
                                        reason="Completed",
                                        exitCode=0
                                    ),
                                )
                            )
                        ]
                    )
```

### The Logs request

When receiving the LogRequest, there are many log options to satisfy, in any
case the response is a byte array. Here the basic example:

```python
    def Logs(self, req: interlink.LogRequest) -> bytes:
        # We are not managing more complicated multi container pod
        #       THIS IS ONLY FOR DEMONSTRATION
        try:
            # Get the container in the request and collect the logs
            container = self.DOCKER.containers.get(self.CONTAINER_POD_MAP[req.PodUID][0])
            #log = container.logs(timestamps=req.Opts.Timestamps, tail=req.Opts.Tail)
            log = container.logs()
            print(log)
        except:
            raise HTTPException(status_code=404, detail="No containers found for UUID")

        return log
```

### A more advanced example

If you are interested in a more advanced example, please refer the
[full example](https://github.com/interlink-hq/interLink/blob/main/example/provider_demo.py)
for supporting configMap and secret volumes.

## Let's test is out

After the completion of
[the core components deployment](./01-deploy-interlink.mdx), you can now
kickstart the newly created plugin and make it spawn on the port 4000 so it can
be contacted by the interLink API server.

You can submit a pod like the following to test the whole workflow:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: interlink-quickstart
  namespace: default
  # # Uncomment this if you are using SLURM
  # annotations:
  #  slurm-job.vk.io/image-root: "docker://"
spec:
  nodeSelector:
    # The name of the virtual node HERE
    kubernetes.io/hostname: civo-node
  automountServiceAccountToken: false
  containers:
    - args:
        - sleep 100 && echo "FINISHED!"
      command:
        - /bin/sh
        - -c
      image: busybox
      imagePullPolicy: Always
      name: my-container
      resources:
        limits:
          cpu: "1"
          memory: 1Gi
        requests:
          cpu: "1"
          memory: 1Gi
  tolerations:
    - key: virtual-node.interlink/no-schedule
      operator: Exists
    - effect: NoExecute
      key: node.kubernetes.io/not-ready
      operator: Exists
      tolerationSeconds: 300
    - effect: NoExecute
      key: node.kubernetes.io/unreachable
      operator: Exists
      tolerationSeconds: 300
```

Finally you should check that all the supported commands (get,logs,delete...)
works on this pod.
