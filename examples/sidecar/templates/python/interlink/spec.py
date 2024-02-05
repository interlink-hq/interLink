from pydantic import BaseModel
from typing import List, Optional

## INIT SERVER --> recover ids
CONTAINER_POD_MAP = {}

class Metadata(BaseModel):
    name: str
    namespace: str
    uid: str
    annotations: dict

class VolumeMount(BaseModel):
    name: str
    mountPath: str
    subPath: Optional[str] = None

class Container(BaseModel):
    name: str
    image: str
    tag: str = 'latest'
    command: List[str]
    args: List[str]
    resources: dict
    volumeMounts: List[VolumeMount]

class SecretSource(BaseModel):
    secretName: str
    items: List[dict] 

class ConfigMapSource(BaseModel):
    configMapName: str
    items: List[dict] 

class VolumeSource(BaseModel):
    emptyDir: Optional[dict] = None
    secret: Optional[SecretSource] = None 
    configMap: Optional[ConfigMapSource] = None 

class PodVolume(BaseModel):
    name: str
    volumeSource: Optional[VolumeSource] = None 

class PodSpec(BaseModel):
    containers: List[Container]
    initContainers: Optional[List[Container]] = None
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
    configMaps: Optional[List[ConfigMap]] = None
    secrets: Optional[List[Secret]] = None
    emptyDirs: Optional[List[str]] = None

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
    terminated: Optional[StateTerminated] = None 
    running: Optional[StateRunning] = None
    waiting: Optional[StateWaiting] = None 

class ContainerStatus(BaseModel):
    name: str
    state: ContainerStates

class PodStatus(BaseModel):
    name: str 
    UID: str
    namespace: str
    containers: List[ContainerStatus]


