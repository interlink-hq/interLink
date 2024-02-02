from pydantic import BaseModel
from typing import List, Optional

## INIT SERVER --> recover ids
CONTAINER_POD_MAP = {}

class Metadata(BaseModel):
    name: str
    namespace: str
    uuid: str
    annotations: List[str]

class Container(BaseModel):
    name: str
    image: str
    tag: str
    command: List[str]
    args: List[str]
    resources: dict

class SecretSource(BaseModel):
    secretName: str
    items: List[dict] 

class ConfigMapSource(BaseModel):
    secretName: str
    items: List[dict] 

class VolumeSource(BaseModel):
    emptyDir: Optional[dict] 
    secret: Optional[SecretSource] 
    configMap: Optional[ConfigMapSource] 

class PodVolume(BaseModel):
    name: str
    volumeSource: VolumeSource 

class PodSpec(BaseModel):
    containers: List[Container]
    initContainers: List[Container]
    volumes: List[PodVolume]

class PodRequest(BaseModel):
    metadata: Metadata
    spec: PodSpec

class ConfigMap(BaseModel):
    metadata: Metadata
    data: dict 

class Secret(BaseModel):
    metadata: Metadata
    data: dict 

class Volume(BaseModel):
    name: str
    configMaps: List[ConfigMap]
    secrets: List[Secret]
    emptyDirs: List[str]

class Pod(BaseModel):
    pod: PodRequest
    container: List[Volume]

class StateTerminated(BaseModel):
    exitCode: int
    reason: str    

class StateRunning(BaseModel):
    startedAt: str    

class StateWaiting(BaseModel):
    message: str
    reason: str    

class ContainerStates(BaseModel):
    terminated: Optional[StateTerminated] 
    running: Optional[StateRunning]
    waiting: Optional[StateWaiting] 

class ContainerStatus(BaseModel):
    name: str
    state: ContainerStates

class PodStatus(BaseModel):
    name: str 
    UID: str
    namespace: str
    containers: List[ContainerStatus]


