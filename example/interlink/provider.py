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

    def Create(self, pod: Pod) -> CreateStruct:
        raise HTTPException(status_code=500, detail="NOT IMPLEMENTED YET")

    def Delete(self, pod: PodRequest) -> None:
        raise HTTPException(status_code=500, detail="NOT IMPLEMENTED YET")

    def Status(self, pod: PodRequest) -> PodStatus:  
        raise HTTPException(status_code=500, detail="NOT IMPLEMENTED YET")

    def Logs(self, req: LogRequest) -> bytes:  
        raise HTTPException(status_code=500, detail="NOT IMPLEMENTED YET")

    def create_pod(self, pod: Pod) -> CreateStruct:
        try:
            self.Create(pod)
        except Exception as ex:
            raise ex

        return "Containers created"

    def delete_pod(self, pod: PodRequest) -> str:
        try:
            self.Delete(pod)
        except Exception as ex:
            raise ex

        return "Containers deleted"

    def get_status(self, pods: List[PodRequest]) -> List[PodStatus]:
        pod = pods[0]

        return [self.Status(pod)]

    def get_logs(self, req: LogRequest) -> bytes:
        try:
            logContent = self.Logs(req)
        except Exception as ex:
            raise ex

        return logContent
