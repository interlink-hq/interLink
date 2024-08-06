import interlink

from fastapi.responses import PlainTextResponse
from fastapi import FastAPI, HTTPException
from typing import List
import docker
import re
import os


docker_client = docker.DockerClient()
# dockerCLI = docker.DockerClient(base_url="unix:///Users/dciangot/.docker/run/docker.sock")

app = FastAPI()


class MyProvider(interlink.provider.Provider):
    def __init__(self, docker):
        super().__init__(docker)

        # Recover already running containers refs
        self.container_pod_map = {}
        statuses = self.docker.api.containers(all=True)
        for status in statuses:
            name = status["Names"][0]
            if len(name.split("-")) > 1:
                uid = "-".join(name.split("-")[-5:])
                self.container_pod_map.update({uid: [status["Id"]]})
        print(self.container_pod_map)

    def dump_volumes(
        self, pods: List[interlink.PodVolume], volumes: List[interlink.Volume]
    ) -> List[str]:

        dataList = []

        # Match data source information (actual bytes) to the mount ref in pod description
        for v in volumes:
            if v.config_maps:
                for data_source in v.config_maps:
                    for ref in pods:
                        pod_mount = ref.volume_source.config_map
                        if pod_mount:
                            if ref.name == data_source.metadata.name:
                                for filename, content in data_source.data.items():
                                    # write content to file
                                    path = f"{data_source.metadata.namespace}-{data_source.metadata.name}/{filename}"
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

            if v.empty_dirs:
                pass
        return dataList

    def create(self, pod: interlink.Pod) -> None:
        container = pod.pod.spec.containers[0]

        if pod.pod.spec.volumes:
            _ = self.dump_volumes(pod.pod.spec.volumes, pod.container)

        volumes = []
        if container.volume_mounts:
            for mount in container.volume_mounts:
                if mount.sub_path:
                    volumes.append(
                        f"{pod.pod.metadata.namespace}-{mount.name}/{mount.sub_path}:{mount.mount_path}"
                    )
                else:
                    volumes.append(
                        f"{pod.pod.metadata.namespace}-{mount.name}:{mount.mount_path}"
                    )

        try:
            cmds = " ".join(container.command)
            args = " ".join(container.args)
            docker_container = self.docker.containers.run(
                f"{container.image}:{container.tag}",
                f"{cmds} {args}",
                name=f"{container.name}-{pod.pod.metadata.uid}",
                detach=True,
                volumes=volumes,
                # runtime="nvidia",
                # device_requests=[
                #           docker.types.DeviceRequest(device_ids=["0"], capabilities=[['gpu']])]
            )
            print(docker_container)
            docker_run_id = docker_container.id
        except Exception as ex:
            raise HTTPException(status_code=500, detail=ex)

        self.container_pod_map.update({pod.pod.metadata.uid: [docker_run_id]})
        print(self.container_pod_map)

        print(pod)

    def delete(self, pod: interlink.PodRequest) -> None:
        try:
            print(f"docker rm -f {self.container_pod_map[pod.metadata.uid][0]}")
            container = self.docker.containers.get(
                self.container_pod_map[pod.metadata.uid][0]
            )
            container.remove(force=True)
            self.container_pod_map.pop(pod.metadata.uid)
        except:
            raise HTTPException(status_code=404, detail="No containers found for UUID")
        print(pod)
        return

    def status(self, pod: interlink.PodRequest) -> interlink.PodStatus:
        print(self.container_pod_map)
        print(pod.metadata.uid)
        try:
            container = self.docker.containers.get(
                self.container_pod_map[pod.metadata.uid][0]
            )
            status = container.status
        except:
            raise HTTPException(status_code=404, detail="No containers found for UUID")

        print(status)

        if status == "running":
            try:
                statuses = self.docker.api.containers(
                    filters={"status": "running", "id": container.id}
                )
                print(statuses)
                started_at = statuses[0]["Created"]
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
                            running=interlink.StateRunning(started_at=started_at),
                            waiting=None,
                            terminated=None,
                        ),
                    )
                ],
            )
        elif status == "exited":

            try:
                statuses = self.docker.api.containers(
                    filters={"status": "exited", "id": container.id}
                )
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
        print(req.pod_uid)
        print(self.container_pod_map[req.pod_uid])
        try:
            container = self.docker.containers.get(
                self.container_pod_map[req.pod_uid][0]
            )
            # log = container.logs(timestamps=req.Opts.Timestamps, tail=req.Opts.Tail)
            log = container.logs()
            print(log)
        except:
            raise HTTPException(status_code=404, detail="No containers found for UUID")
        return log


provider_new = MyProvider(docker_client)


@app.post("/create")
async def create_pod(pods: List[interlink.Pod]) -> str:
    return provider_new.create_pod(pods)


@app.post("/delete")
async def delete_pod(pod: interlink.PodRequest) -> str:
    return provider_new.delete_pod(pod)


@app.get("/status")
async def status_pod(pods: List[interlink.PodRequest]) -> List[interlink.PodStatus]:
    return provider_new.get_status(pods)


@app.get("/getLogs", response_class=PlainTextResponse)
async def get_logs(req: interlink.LogRequest) -> bytes:
    return provider_new.get_logs(req)