import interlink

from fastapi.responses import PlainTextResponse
from fastapi import FastAPI, HTTPException
from typing import List
import docker
import re
import os


class MockRuntime():
    def __init__(self):
        self.CONTAINERS = {}

    def create(container,cmds, args, volumes) -> str:

        print(f"image: {container.image}:{container.tag}")
        print(f"command: {cmds}\nargs: {args}")
        print(f"volumes: {volumes}") 
        print(f"env: {container.env}") 

        container_id = uuid()

        self.CONTAINERS.update(
            {
                "id": container_id,
                "start_time": now(), 
                "status": "Pending"
            }
        )
        return container_id 

    def status(container_id) -> str:
        return self.CONTAINERS[container_id]

    def delete(container_id) -> bool:
        return true


#dockerCLI = docker.DockerClient()
# dockerCLI = docker.DockerClient(base_url="unix:///Users/dciangot/.docker/run/docker.sock")

app = FastAPI()

runtime = MockRuntime()

class MyProvider(interlink.provider.Provider):
    def __init__(self, DOCKER):
        super().__init__(DOCKER)

        # Recover already running containers refs
        self.CONTAINER_POD_MAP = {}

    def DumpVolumes(
        self, pods: List[interlink.PodVolume], volumes: List[interlink.Volume]
    ) -> List[str]:

        dataList = []

        # Match data source information (actual bytes) to the mount ref in pod description
        for v in volumes:
            if v.configMaps:
                for dataSource in v.configMaps:
                    for ref in pods:
                        podMount = ref.volumeSource.configMap
                        if podMount:
                            if ref.name == dataSource.metadata.name:
                                for filename, content in dataSource.data.items():
                                    # write content to file
                                    path = f"{dataSource.metadata.namespace}-{dataSource.metadata.name}/{filename}"
                                    try:
                                        os.makedirs(
                                            os.path.dirname(path), exist_ok=True
                                        )
                                        with open(path, "w") as f:
                                            f.write(content)
                                    except Exception as ex:
                                        raise HTTPException(status_code=500, detail=ex)

                                    # dump list of written files
                                    dataList.append(path)

            if v.secrets:
                pass

            if v.emptyDirs:
                pass
        return dataList

    def Create(self, pod: interlink.Pod) -> None:
        container = pod.pod.spec.containers[0]

        if pod.pod.spec.volumes:
            _ = self.DumpVolumes(pod.pod.spec.volumes, pod.container)

        volumes = []
        if container.volumeMounts:
            for mount in container.volumeMounts:
                if mount.subPath:
                    volumes.append(
                        f"{pod.pod.metadata.namespace}-{mount.name}/{mount.subPath}:{mount.mountPath}"
                    )
                else:
                    volumes.append(
                        f"{pod.pod.metadata.namespace}-{mount.name}:{mount.mountPath}"
                    )

        try:
            cmds = " ".join(container.command)
            args = " ".join(container.args)
            docker_run_id = self.DOCKER.run(
                container,
                cmds,
                args,
                volumes=volumes,
            )
        except Exception as ex:
            raise HTTPException(status_code=500, detail=ex)

        self.CONTAINER_POD_MAP.update({pod.pod.metadata.uid: [docker_run_id]})
        print(self.CONTAINER_POD_MAP)

        print(pod)

    def Delete(self, pod: interlink.PodRequest) -> None:
        try:
            print(f"docker rm -f {self.CONTAINER_POD_MAP[pod.metadata.uid][0]}")
            container.remove(self.CONTAINER_POD_MAP[pod.metadata.uid][0])
            self.CONTAINER_POD_MAP.pop(pod.metadata.uid)
        except:
            raise HTTPException(status_code=404, detail="No containers found for UUID")
        return

    def Status(self, pod: interlink.PodRequest) -> interlink.PodStatus:
        print(self.CONTAINER_POD_MAP)
        print(pod.metadata.uid)
        try:
            status = self.DOCKER.containers.status(
                self.CONTAINER_POD_MAP[pod.metadata.uid][0]
            )
            status = container.status
        except:
            raise HTTPException(status_code=404, detail="No containers found for UUID")

        if status == "running":
            try:
                startedAt = statuses[0]["Created"]
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
                        ),
                    )
                ],
            )
        elif status == "exited":

            try:
                print(statuses)
                reason = statuses[0]["Status"]
                pattern = re.compile(r"Exited \((.*?)\)")

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
                                reason=reason, exitCode=exitCode
                            ),
                        ),
                    )
                ],
            )

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
                            reason="Completed", exitCode=0
                        ),
                    ),
                )
            ],
        )

    def Logs(self, req: interlink.LogRequest) -> bytes:
        # TODO: manage more complicated multi container pod
        #       THIS IS ONLY FOR DEMONSTRATION
        print(req.PodUID)
        print(self.CONTAINER_POD_MAP[req.PodUID])
        try:
            container = self.DOCKER.containers.get(
                self.CONTAINER_POD_MAP[req.PodUID][0]
            )
            # log = container.logs(timestamps=req.Opts.Timestamps, tail=req.Opts.Tail)
            log = container.logs()
            print(log)
        except:
            raise HTTPException(status_code=404, detail="No containers found for UUID")
        return log


ProviderNew = MyProvider(MockRuntime)


@app.post("/create")
async def create_pod(pods: List[interlink.Pod]) -> str:
    return ProviderNew.create_pod(pods)


@app.post("/delete")
async def delete_pod(pod: interlink.PodRequest) -> str:
    return ProviderNew.delete_pod(pod)


@app.get("/status")
async def status_pod(pods: List[interlink.PodRequest]) -> List[interlink.PodStatus]:
    return ProviderNew.get_status(pods)


@app.get("/getLogs", response_class=PlainTextResponse)
async def get_logs(req: interlink.LogRequest) -> bytes:
    return ProviderNew.get_logs(req)
