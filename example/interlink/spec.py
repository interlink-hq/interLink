from pydantic import BaseModel, Field
import datetime
from typing import List, Optional

class Metadata(BaseModel):
    name: str
    namespace: str
    uid: str
    annotations: Optional[dict] = Field({})

class VolumeMount(BaseModel):
    name: str
    mountPath: str
    subPath: Optional[str] = None
    readOnly: Optional[bool] = False

class Container(BaseModel):
    name: str
    image: str
    tag: str = 'latest'
    command: List[str]
    args: Optional[List[str]] = Field([])
    resources: Optional[dict] = Field({})
    volumeMounts: Optional[List[VolumeMount]] = Field([])

class SecretSource(BaseModel):
    secretName: str
    items: List[dict] 

class ConfigMapSource(BaseModel):
    configMapName: str
    items: List[dict] 

# class VolumeSource(BaseModel):
#     emptyDir: Optional[dict] = None
#     secret: Optional[SecretSource] = None
#     configMap: Optional[ConfigMapSource] = None

class PodVolume(BaseModel):
    name: str
#    volumeSource: Optional[VolumeSource] = None
    emptyDir: Optional[dict] = None
    secret: Optional[SecretSource] = None
    configMap: Optional[ConfigMapSource] = None

class PodSpec(BaseModel):
    containers: List[Container]
    initContainers: Optional[List[Container]] = None
    volumes: Optional[List[PodVolume]] = None

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
    reason: Optional[str] = None

class StateRunning(BaseModel):
    startedAt: Optional[str] = None

class StateWaiting(BaseModel):
    message: Optional[str] = None
    reason: Optional[str] = None

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

class LogOpts(BaseModel):
    Tail: Optional[int] = None
    LimitBytes: Optional[int] = None
    Timestamps: Optional[bool] = None
    Previous: Optional[bool] = None
    SinceSeconds: Optional[int] = None
    SinceTime: Optional[datetime.datetime] = None

class LogRequest(BaseModel):
    Namespace: str
    PodUID: str
    PodName: str
    ContainerName: str
    Opts: LogOpts

