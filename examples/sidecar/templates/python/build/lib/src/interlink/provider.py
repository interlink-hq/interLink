from fastapi import FastAPI, HTTPException
from pydantic import BaseModel
from typing import List, Optional
import docker
from .spec import *
#import kubernetes
#print(kubernetes.client.ApisApi().api_client.

## INIT SERVER --> recover ids
CONTAINER_POD_MAP = {}

DOCKER = docker.DockerClient(base_url="unix:///Users/dciangot/.docker/run/docker.sock")

app = FastAPI()

@app.post("/create")
async def create_pod(pods: List[Pod]) -> str:
    pod = pods[0]

    container = pod.pod.spec.containers[0]

    try:
        cmds = " ".join(container.command)
        args = " ".join(container.args)
        dockerContainer = DOCKER.containers.run(
            f"{container.image}:{container.tag}",
            f"{cmds} {args}",
            name=f"{container.name}-{pod.pod.metadata.uuid}",
            detach=True
        )
        docker_run_id = dockerContainer.id
    except Exception as ex:
        raise HTTPException(status_code=500, detail=ex)


    CONTAINER_POD_MAP.update({pod.pod.metadata.uuid: [docker_run_id]})
    print(CONTAINER_POD_MAP)

    return "Containers created"

@app.post("/delete")
async def delete_pod(pods: List[Pod]) -> str:
    pod = pods[0]

    try:
        print(f"docker rm -f {CONTAINER_POD_MAP[pod.pod.metadata.uuid][0]}")
        container = DOCKER.containers.get(CONTAINER_POD_MAP[pod.pod.metadata.uuid][0])
        container.remove(force=True)
        CONTAINER_POD_MAP.pop(pod.pod.metadata.uuid)
    except:
        raise HTTPException(status_code=404, detail="No containers found for UUID")

    return "Containers deleted"


@app.post("/status")
async def get_status(pods: List[PodRequest]) -> List[PodStatus]:
    pod = pods[0]

    print(CONTAINER_POD_MAP)
    try:
        container = DOCKER.containers.get(CONTAINER_POD_MAP[pod.metadata.uuid][0])
        status = container.status
    except:
        raise HTTPException(status_code=404, detail="No containers found for UUID")

    print(status)

    if status == "running":
        try:
            statuses = DOCKER.api.containers(filters={"status":"exited", "id": container.id})
            print(statuses)
            startedAt = statuses[0]["Created"]
        except Exception as ex:
            raise HTTPException(status_code=500, detail=ex)

        return [
            PodStatus(
                name=pod.metadata.name,
                UID=pod.metadata.uuid,
                namespace=pod.metadata.namespace,
                containers=[
                    ContainerStatus(
                        name=pod.spec.containers[0].name,
                        state=ContainerStates(
                            running=StateRunning(startedAt=startedAt),
                            waiting=None,
                            terminated=None,
                        )
                    )
                ]
            )
        ]
    elif status == "exited":

        try:
            statuses = DOCKER.api.containers(filters={"status":"exited", "id": container.id})
            print(statuses)
            reason = statuses[0]["Status"]
            import re
            pattern = re.compile(r'Exited \((.*?)\)')

            exitCode = -1
            for match in re.findall(pattern, reason):
                exitCode = int(match)
        except Exception as ex:
            raise HTTPException(status_code=500, detail=ex)
            
        return [
            PodStatus(
                name=pod.metadata.name,
                UID=pod.metadata.uuid,
                namespace=pod.metadata.namespace,
                containers=[
                    ContainerStatus(
                        name=pod.spec.containers[0].name,
                        state=ContainerStates(
                            running=None,
                            waiting=None,
                            terminated=StateTerminated(
                                reason=reason,
                                exitCode=exitCode
                            ),
                        )
                    )
                ]
            )
        ]
        
    return [
        PodStatus(
            name=pod.metadata.name,
            UID=pod.metadata.uuid,
            namespace=pod.metadata.namespace,
            containers=[
                ContainerStatus(
                    name=pod.spec.containers[0].name,
                    state=ContainerStates(
                        running=None,
                        waiting=None,
                        terminated=StateTerminated(
                            reason="Completed",
                            exitCode=0
                        ),
                    )
                )
            ]
        )
    ]

