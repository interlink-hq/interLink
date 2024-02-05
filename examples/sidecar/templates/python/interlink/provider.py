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

    def Create(self, pod: Pod) -> None:
        raise HTTPException(status_code=404, detail="No containers found for UUID")

    def Delete(self, pod: PodRequest) -> None:
        raise HTTPException(status_code=404, detail="No containers found for UUID")

    def create_pod(self, pods: List[Pod]) -> str:
        pod = pods[0]

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


    def Status(self, pod: PodRequest) -> PodStatus:  

        raise HTTPException(status_code=404, detail="No containers found for UUID")


    def get_status(self, pods: List[PodRequest]) -> List[PodStatus]:
        pod = pods[0]

        return [self.Status(pod)]


