import datetime
from typing import Dict, List, Optional

from pydantic import BaseModel, Field

class Metadata(BaseModel):
    name: Optional[str] = None
    namespace: Optional[str] = None
    uid: Optional[str] = None
    annotations: Optional[Dict[str, str]] = Field({})
    labels: Optional[Dict[str, str]] = Field({})
    generate_name: Optional[str] = None


class VolumeMount(BaseModel):
    name: str
    mount_path: str
    sub_path: Optional[str] = None
    read_only: Optional[bool] = False
    mount_propagation: Optional[str] = None


class ConfigMapKeySelector(BaseModel):
    key: str
    name: Optional[str] = None
    optional: Optional[bool] = None


class SecretKeySelector(BaseModel):
    key: str
    name: Optional[str] = None
    optional: Optional[bool] = None


class EnvVarSource(BaseModel):
    config_map_key_ref: Optional[ConfigMapKeySelector] = None
    secret_key_ref: Optional[SecretKeySelector] = None


class EnvVar(BaseModel):
    name: str
    value: Optional[str] = None
    value_from: Optional[EnvVarSource] = None


class SecurityContext(BaseModel):
    allow_privilege_escalation: Optional[bool] = None
    privileged: Optional[bool] = None
    proc_mount: Optional[str] = None
    read_only_file_system: Optional[bool] = None
    run_as_group: Optional[int] = None
    run_as_non_root: Optional[bool] = None
    run_as_user: Optional[int] = None


class Container(BaseModel):
    name: str
    image: str
    tag: str = "latest"
    command: List[str]
    args: Optional[List[str]] = Field([])
    resources: Optional[dict] = Field({})
    volume_mounts: Optional[List[VolumeMount]] = Field([])
    env: Optional[List[EnvVar]] = None
    security_context: Optional[SecurityContext] = None


class KeyToPath(BaseModel):
    key: Optional[str]
    path: str
    mode: Optional[int] = None


class SecretVolumeSource(BaseModel):
    secret_name: str
    items: Optional[List[KeyToPath]] = Field([])
    optional: Optional[bool] = None
    default_mode: Optional[int] = None


class ConfigMapVolumeSource(BaseModel):
    name: str
    items: Optional[List[KeyToPath]] = Field([])
    optional: Optional[bool] = None
    default_mode: Optional[int] = None


# class VolumeSource(BaseModel):
#     emptyDir: Optional[dict] = None
#     secret: Optional[SecretSource] = None
#     configMap: Optional[ConfigMapVolumeSource] = None


class PodVolume(BaseModel):
    name: str
    #    volumeSource: Optional[VolumeSource] = None
    empty_dir: Optional[dict] = None
    secret: Optional[SecretVolumeSource] = None
    config_map: Optional[ConfigMapVolumeSource] = None


class PodSpec(BaseModel):
    containers: List[Container]
    init_containers: Optional[List[Container]] = None
    volumes: Optional[List[PodVolume]] = None
    preemption_policy: Optional[str] = None
    priority_class_name: Optional[str] = None
    priority: Optional[int] = None
    restart_policy: Optional[str] = None
    termination_grace_period_seconds: Optional[int] = None


class PodRequest(BaseModel):
    metadata: Metadata
    spec: PodSpec


class ConfigMap(BaseModel):
    metadata: Metadata
    data: Optional[dict]
    binary_data: Optional[dict] = None
    type: Optional[str] = None
    immutable: Optional[bool] = None


class Secret(BaseModel):
    metadata: Metadata
    data: Optional[dict] = None
    string_data: Optional[dict] = None
    type: Optional[str] = None
    immutable: Optional[bool] = None


class Volume(BaseModel):
    name: str
    config_maps: Optional[List[ConfigMap]] = None
    secrets: Optional[List[Secret]] = None
    empty_dirs: Optional[List[str]] = None


class Pod(BaseModel):
    pod: PodRequest
    container: List[Volume]


class StateTerminated(BaseModel):
    exit_code: int
    reason: Optional[str] = None


class StateRunning(BaseModel):
    started_at: Optional[str] = None


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
    uid: str
    namespace: str
    containers: List[ContainerStatus]


class LogOpts(BaseModel):
    tail: Optional[int] = None
    limit_bytes: Optional[int] = None
    timestamps: Optional[bool] = None
    previous: Optional[bool] = None
    since_seconds: Optional[int] = None
    since_time: Optional[datetime.datetime] = None


class LogRequest(BaseModel):
    namespace: str
    pod_uid: str
    pod_name: str
    container_name: str
    opts: LogOpts

class CreateStruct(BaseModel):
	pod_uid: str
	pod_jid: str