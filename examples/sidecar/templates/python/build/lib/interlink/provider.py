from fastapi import FastAPI, HTTPException
from .spec import * 
from typing import List


class Provider(FastAPI):
    def __init__(
        self,
        docker_client,
    ):
        self.DOCKER = docker_client
        self.CONTAINER_POD_MAP = {}

    def Create(self, pod: Pod):

        container = pod.pod.spec.containers[0]

        try:
            cmds = " ".join(container.command)
            args = " ".join(container.args)
            dockerContainer = self.DOCKER.containers.run(
                f"{container.image}:{container.tag}",
                f"{cmds} {args}",
                name=f"{container.name}-{pod.pod.metadata.uuid}",
                detach=True
            )
            docker_run_id = dockerContainer.id
        except Exception as ex:
            raise HTTPException(status_code=500, detail=ex)


        self.CONTAINER_POD_MAP.update({pod.pod.metadata.uuid: [docker_run_id]})
        print(self.CONTAINER_POD_MAP)

        print(pod)
        return 

    def Delete(self, pod: Pod):
        try:
            print(f"docker rm -f {self.CONTAINER_POD_MAP[pod.pod.metadata.uuid][0]}")
            container = self.DOCKER.containers.get(self.CONTAINER_POD_MAP[pod.pod.metadata.uuid][0])
            container.remove(force=True)
            self.CONTAINER_POD_MAP.pop(pod.pod.metadata.uuid)
        except:
            raise HTTPException(status_code=404, detail="No containers found for UUID")
        return

    def create_pod(self, pods: List[Pod]) -> str:
        pod = pods[0]

        try:
            self.Create(pod)
        except Exception as ex:
            raise ex

        return "Containers created"

    def delete_pod(self, pods: List[Pod]) -> str:
        pod = pods[0]

        try:
            self.Delete(pod)
        except Exception as ex:
            raise ex

        return "Containers deleted"


    def Status(self, pod: PodRequest) -> PodStatus:  
        print(self.CONTAINER_POD_MAP)
        try:
            container = self.DOCKER.containers.get(CONTAINER_POD_MAP[pod.metadata.uuid][0])
            status = container.status
        except:
            raise HTTPException(status_code=404, detail="No containers found for UUID")

        print(status)

        if status == "running":
            try:
                statuses = self.DOCKER.api.containers(filters={"status":"exited", "id": container.id})
                print(statuses)
                startedAt = statuses[0]["Created"]
            except Exception as ex:
                raise HTTPException(status_code=500, detail=ex)

            return PodStatus(
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
        elif status == "exited":

            try:
                statuses = self.DOCKER.api.containers(filters={"status":"exited", "id": container.id})
                print(statuses)
                reason = statuses[0]["Status"]
                import re
                pattern = re.compile(r'Exited \((.*?)\)')

                exitCode = -1
                for match in re.findall(pattern, reason):
                    exitCode = int(match)
            except Exception as ex:
                raise HTTPException(status_code=500, detail=ex)
                
            return PodStatus(
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
            
        return PodStatus(
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



    def get_status(self, pods: List[PodRequest]) -> List[PodStatus]:
        pod = pods[0]

        return [self.Status(pod)]


