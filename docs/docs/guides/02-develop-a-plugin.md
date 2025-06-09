---
sidebar_position: 2
---

# Develop an interLink plugin

Learn how to develop your interLink plugin to manage containers on your remote
host.

We are going to follow up
[the setup of an interlink node](../cookbook/1-edge.mdx) with the last piece of
the puzzle:

- setup of a python SDK
- demoing the fundamentals development of a plugin executing containers locally
  through the host docker daemon

:::warning

The python SDK also produce an openAPI spec through FastAPI, therefore you can
use any language you want as long as the API spec is satisfied.

:::

## Plugin API Specification

Before diving into development, familiarize yourself with the complete plugin
API specification. The OpenAPI specification defines all the endpoints,
request/response schemas, and data types your plugin must implement:

📋 **[Plugin OpenAPI Specification](../../../openapi/plugin-openapi.json)**

This specification is the authoritative reference for:

- Required HTTP endpoints (`/create`, `/delete`, `/status`, `/getLogs`)
- Request and response data structures
- Error handling and status codes
- Authentication requirements

Any plugin implementation in any programming language must comply with this API
specification to work with interLink.

## Setup the python SDK

### Requirements

- The tutorial is done on a Ubuntu VM, but there are not hard requirements
  around that
- Python>=3.10 and pip (`sudo apt install -y python3-pip`)
- Any python IDE will work and it is strongly suggested to use one :)
- A [docker engine running](https://docs.docker.com/engine/install/)

### Install the SDK

Look for the latest release on
[the release page](https://github.com/interlink-hq/interLink/releases) and set
the environment variable `VERSION` to it. Then you are ready to install the
python SDK with:

```bash
#export VERSION=X.X.X
#pip install "uvicorn[standard]" "git+https://github.com/interlink-hq/interlink-plugin-sdk@${VERSION}"

# Or download the latest one with
pip install "uvicorn[standard]" "git+https://github.com/interlink-hq/interlink-plugin-sdk"

```

## Understanding the Plugin Architecture

InterLink plugins act as "sidecar" containers that handle the actual execution
of workloads on remote resources. The plugin communicates with the interLink API
server via REST endpoints and translates Kubernetes pod specifications into
commands suitable for your target infrastructure.

### Core Data Structures

The plugin interface uses several key data structures defined in the interLink
types:

#### PodCreateRequests

```json
{
    "pod": {...},           // Standard Kubernetes Pod spec
    "configmaps": [...],    // Associated ConfigMaps
    "secrets": [...],       // Associated Secrets
    "projectedvolumesmaps": [...],  // ServiceAccount projected volumes
    "jobscriptURL": ""      // Optional job script builder endpoint
}
```

#### PodStatus

```json
{
    "name": "pod-name",
    "UID": "pod-uid",
    "namespace": "default",
    "JID": "remote-job-id",
    "containers": [...],     // Container status array
    "initContainers": [...]  // Init container status array
}
```

#### CreateStruct

```json
{
  "PodUID": "kubernetes-pod-uid",
  "PodJID": "remote-system-job-id"
}
```

## Plugin Interface Requirements

Your plugin must implement the following REST API endpoints:

### POST /create

Creates one or more pods on the remote system.

**Request Body**: `List[PodCreateRequests]` **Response**: `List[CreateStruct]`

### POST /delete

Deletes a pod from the remote system.

**Request Body**: `PodStatus` **Response**: Success/error status

### GET /status

Retrieves the current status of one or more pods.

**Query Parameters**: List of pod UIDs **Response**: `List[PodStatus]`

### GET /getLogs

Retrieves logs from a specific container.

**Query Parameters**: Pod UID, container name, log options **Response**:
Container logs (plain text)

## Developing with the Python SDK

### Basic Plugin Structure

Here's a complete example of a Docker-based plugin using the interLink Python
SDK:

```python
import interlink
from fastapi.responses import PlainTextResponse
from fastapi import FastAPI, HTTPException
from typing import List
import docker
import re
import os

# Initialize Docker client
docker_client = docker.DockerClient()
app = FastAPI()

class MyProvider(interlink.provider.Provider):
    def __init__(self, docker):
        super().__init__(docker)
        self.container_pod_map = {}

        # Recover already running containers
        statuses = self.docker.api.containers(all=True)
        for status in statuses:
            name = status["Names"][0]
            if len(name.split("-")) > 1:
                uid = "-".join(name.split("-")[-5:])
                self.container_pod_map.update({uid: [status["Id"]]})

    def create(self, pod: interlink.Pod) -> None:
        """Create a pod by running Docker containers"""
        container = pod.pod.spec.containers[0]

        # Handle volumes if present
        if pod.pod.spec.volumes:
            self.dump_volumes(pod.pod.spec.volumes, pod.container)

        # Set up volume mounts
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
            # Prepare command and arguments
            cmds = " ".join(container.command) if container.command else ""
            args = " ".join(container.args) if container.args else ""

            # Run the container
            docker_container = self.docker.containers.run(
                f"{container.image}",
                f"{cmds} {args}".strip(),
                name=f"{container.name}-{pod.pod.metadata.uid}",
                detach=True,
                volumes=volumes,
                # Add additional Docker options as needed
                # environment=container.env,
                # ports=container.ports,
            )

            # Store container mapping
            self.container_pod_map.update({
                pod.pod.metadata.uid: [docker_container.id]
            })

        except Exception as ex:
            raise HTTPException(status_code=500, detail=str(ex))

    def delete(self, pod: interlink.PodRequest) -> None:
        """Delete a pod by removing its containers"""
        try:
            container_id = self.container_pod_map[pod.metadata.uid][0]
            container = self.docker.containers.get(container_id)
            container.remove(force=True)
            self.container_pod_map.pop(pod.metadata.uid)
        except KeyError:
            raise HTTPException(
                status_code=404,
                detail="No containers found for UUID"
            )

    def status(self, pod: interlink.PodRequest) -> interlink.PodStatus:
        """Get the current status of a pod"""
        try:
            container_id = self.container_pod_map[pod.metadata.uid][0]
            container = self.docker.containers.get(container_id)
            status = container.status
        except KeyError:
            raise HTTPException(
                status_code=404,
                detail="No containers found for UUID"
            )

        # Map Docker status to Kubernetes container status
        if status == "running":
            statuses = self.docker.api.containers(
                filters={"status": "running", "id": container.id}
            )
            started_at = statuses[0]["Created"]

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
            # Extract exit code from status
            statuses = self.docker.api.containers(
                filters={"status": "exited", "id": container.id}
            )
            reason = statuses[0]["Status"]
            pattern = re.compile(r"Exited \((.*?)\)")

            exit_code = -1
            for match in re.findall(pattern, reason):
                exit_code = int(match)

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
                                reason=reason,
                                exitCode=exit_code
                            ),
                        ),
                    )
                ],
            )

        # Default completed status
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
                            reason="Completed",
                            exitCode=0
                        ),
                    ),
                )
            ],
        )

    def Logs(self, req: interlink.LogRequest) -> bytes:
        """Retrieve logs from a container"""
        try:
            container_id = self.container_pod_map[req.pod_uid][0]
            container = self.docker.containers.get(container_id)
            log = container.logs(
                timestamps=req.Opts.Timestamps if hasattr(req.Opts, 'Timestamps') else False,
                tail=req.Opts.Tail if hasattr(req.Opts, 'Tail') else 'all'
            )
            return log
        except KeyError:
            raise HTTPException(
                status_code=404,
                detail="No containers found for UUID"
            )

    def dump_volumes(self, pod_volumes: List, container_volumes: List) -> List[str]:
        """Handle ConfigMaps, Secrets, and other volume types"""
        data_list = []

        for volume in container_volumes:
            # Handle ConfigMaps
            if volume.config_maps:
                for config_map in volume.config_maps:
                    for pod_vol in pod_volumes:
                        if (pod_vol.volume_source.config_map and
                            pod_vol.name == config_map.metadata.name):

                            for filename, content in config_map.data.items():
                                path = f"{config_map.metadata.namespace}-{config_map.metadata.name}/{filename}"
                                os.makedirs(os.path.dirname(path), exist_ok=True)

                                with open(path, "w") as f:
                                    f.write(content)
                                data_list.append(path)

            # Handle Secrets (base64 decode)
            if volume.secrets:
                for secret in volume.secrets:
                    # Similar logic for secrets
                    pass

            # Handle EmptyDirs
            if volume.empty_dirs:
                # Create empty directories
                pass

        return data_list

# Initialize provider
provider = MyProvider(docker_client)

# FastAPI endpoints
@app.post("/create")
async def create_pod(pods: List[interlink.Pod]) -> List[interlink.CreateStruct]:
    return provider.create_pod(pods)

@app.post("/delete")
async def delete_pod(pod: interlink.PodRequest) -> str:
    return provider.delete_pod(pod)

@app.get("/status")
async def status_pod(pods: List[interlink.PodRequest]) -> List[interlink.PodStatus]:
    return provider.get_status(pods)

@app.get("/getLogs", response_class=PlainTextResponse)
async def get_logs(req: interlink.LogRequest) -> bytes:
    return provider.get_logs(req)

# Run the server
if __name__ == "__main__":
    import uvicorn
    uvicorn.run(app, host="0.0.0.0", port=8000)
```

### Advanced Plugin Features

#### Volume Handling

The plugin can handle various Kubernetes volume types:

```python
def handle_persistent_volumes(self, pod_spec):
    """Example of handling PersistentVolumeClaims"""
    for volume in pod_spec.volumes:
        if volume.persistent_volume_claim:
            pvc_name = volume.persistent_volume_claim.claim_name
            # Mount the PVC to your remote system
            self.mount_pvc(pvc_name, volume.name)

def handle_projected_volumes(self, projected_volumes):
    """Handle ServiceAccount tokens and projected volumes"""
    for pv_map in projected_volumes:
        for filename, content in pv_map.data.items():
            # Write ServiceAccount tokens, CA certificates, etc.
            self.write_projected_file(filename, content)
```

#### Resource Management

```python
def apply_resource_limits(self, container_spec, docker_params):
    """Apply CPU and memory limits to containers"""
    if container_spec.resources:
        if container_spec.resources.limits:
            limits = container_spec.resources.limits
            if 'cpu' in limits:
                # Convert Kubernetes CPU units to Docker format
                docker_params['cpu_period'] = 100000
                docker_params['cpu_quota'] = int(float(limits['cpu']) * 100000)
            if 'memory' in limits:
                # Convert memory units (Ki, Mi, Gi)
                docker_params['mem_limit'] = self.parse_memory(limits['memory'])
```

#### Environment Variables and Secrets

```python
def setup_environment(self, container_spec, secrets, config_maps):
    """Set up environment variables from various sources"""
    env_vars = {}

    # Direct environment variables
    for env in container_spec.env or []:
        if env.value:
            env_vars[env.name] = env.value
        elif env.value_from:
            # Handle valueFrom sources
            if env.value_from.secret_key_ref:
                secret_name = env.value_from.secret_key_ref.name
                secret_key = env.value_from.secret_key_ref.key
                env_vars[env.name] = self.get_secret_value(secrets, secret_name, secret_key)
            elif env.value_from.config_map_key_ref:
                cm_name = env.value_from.config_map_key_ref.name
                cm_key = env.value_from.config_map_key_ref.key
                env_vars[env.name] = self.get_configmap_value(config_maps, cm_name, cm_key)

    return env_vars
```

## Testing Your Plugin

### Local Testing

Create a simple test script to verify your plugin endpoints:

```python
import requests
import json

# Test data
test_pod = {
    "pod": {
        "metadata": {"name": "test-pod", "uid": "test-uid", "namespace": "default"},
        "spec": {
            "containers": [{
                "name": "test-container",
                "image": "nginx:latest",
                "command": ["nginx"],
                "args": ["-g", "daemon off;"]
            }]
        }
    },
    "configmaps": [],
    "secrets": [],
    "projectedvolumesmaps": []
}

# Test creation
response = requests.post("http://localhost:8000/create", json=[test_pod])
print(f"Create response: {response.json()}")

# Test status
response = requests.get("http://localhost:8000/status", params={"pod_uid": "test-uid"})
print(f"Status response: {response.json()}")
```

### Integration Testing

Use the interLink test suite to verify your plugin works with the full system:

```bash
# Build your plugin image
docker build -t my-plugin:latest .

# Update plugin configuration
export PLUGIN_IMAGE=my-plugin:latest
export PLUGIN_PORT=8000

# Run integration tests
make test
```

## Deployment and Configuration

### Plugin Configuration

Create a configuration file for your plugin:

```yaml
# plugin-config.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: plugin-config
data:
  plugin.yaml: |
    plugin:
      endpoint: "http://plugin-service:8000"
      authentication:
        type: "bearer"
        token: "your-auth-token"
      timeout: 30s
```

### Kubernetes Deployment

Deploy your plugin as a Kubernetes service:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-plugin
spec:
  replicas: 1
  selector:
    matchLabels:
      app: my-plugin
  template:
    metadata:
      labels:
        app: my-plugin
    spec:
      containers:
        - name: plugin
          image: my-plugin:latest
          ports:
            - containerPort: 8000
          env:
            - name: PLUGIN_CONFIG
              value: "/etc/plugin/config.yaml"
---
apiVersion: v1
kind: Service
metadata:
  name: plugin-service
spec:
  selector:
    app: my-plugin
  ports:
    - port: 8000
      targetPort: 8000
```

## Real-World Examples

### SLURM Plugin

For HPC workloads using SLURM:

```python
class SLURMProvider(interlink.provider.Provider):
    def create(self, pod: interlink.Pod) -> None:
        # Convert pod spec to SLURM job script
        job_script = self.generate_slurm_script(pod)

        # Submit to SLURM
        result = subprocess.run(
            ["sbatch", "--parsable"],
            input=job_script,
            capture_output=True,
            text=True
        )

        job_id = result.stdout.strip()
        self.job_pod_map[pod.pod.metadata.uid] = job_id

    def generate_slurm_script(self, pod):
        container = pod.pod.spec.containers[0]
        return f"""#!/bin/bash
#SBATCH --job-name={pod.pod.metadata.name}
#SBATCH --output=job_%j.out
#SBATCH --error=job_%j.err

# Run container with Singularity/Apptainer
singularity exec {container.image} {' '.join(container.command or [])}
"""
```

### Cloud Provider Plugin

For cloud platforms like AWS ECS or Google Cloud Run:

```python
class CloudProvider(interlink.provider.Provider):
    def create(self, pod: interlink.Pod) -> None:
        # Convert to cloud-native format
        task_definition = self.pod_to_task_definition(pod)

        # Submit to cloud provider
        response = self.cloud_client.run_task(
            taskDefinition=task_definition,
            cluster=self.cluster_name
        )

        task_arn = response['tasks'][0]['taskArn']
        self.task_pod_map[pod.pod.metadata.uid] = task_arn
```

### Kubernetes Plugin (Cross-Cluster)

Based on the
[interLink Kubernetes Plugin](https://github.com/interlink-hq/interlink-kubernetes-plugin):

```python
class KubernetesProvider(interlink.provider.Provider):
    def __init__(self, remote_kubeconfig):
        super().__init__()
        self.k8s_client = kubernetes.client.ApiClient(
            kubernetes.config.load_kube_config(remote_kubeconfig)
        )
        self.core_v1 = kubernetes.client.CoreV1Api(self.k8s_client)

    def create(self, pod: interlink.Pod) -> None:
        # Handle volume offloading
        self.sync_volumes(pod)

        # Handle microservice offloading with TCP tunnels
        if self.has_exposed_ports(pod):
            self.setup_tcp_tunnel(pod)

        # Create pod on remote cluster
        try:
            response = self.core_v1.create_namespaced_pod(
                namespace=pod.pod.metadata.namespace,
                body=pod.pod
            )
            self.pod_map[pod.pod.metadata.uid] = response.metadata.name
        except kubernetes.client.ApiException as e:
            raise HTTPException(status_code=500, detail=str(e))

    def sync_volumes(self, pod):
        """Sync ConfigMaps, Secrets, and PVCs to remote cluster"""
        for volume in pod.container:
            if volume.config_maps:
                for cm in volume.config_maps:
                    self.create_or_update_configmap(cm)
            if volume.secrets:
                for secret in volume.secrets:
                    self.create_or_update_secret(secret)
```

## Best Practices

1. **Error Handling**: Always provide meaningful error messages and appropriate
   HTTP status codes
2. **Logging**: Implement comprehensive logging for debugging and monitoring
3. **Resource Cleanup**: Ensure proper cleanup of resources when pods are
   deleted
4. **State Persistence**: Consider persisting plugin state to handle restarts
5. **Security**: Implement proper authentication and authorization for your
   plugin endpoints
6. **Monitoring**: Add health checks and metrics endpoints for observability
7. **Idempotency**: Make operations idempotent to handle retries gracefully
8. **Resource Limits**: Always respect and enforce Kubernetes resource limits
9. **Graceful Shutdown**: Handle SIGTERM signals for graceful container shutdown

## Running Your Plugin

### Development Mode

```bash
# Install dependencies
pip install -r requirements.txt

# Run with auto-reload
uvicorn main:app --reload --host 0.0.0.0 --port 8000
```

### Production Mode

```bash
# Build container
docker build -t my-plugin:v1.0.0 .

# Run container
docker run -d \
  --name my-plugin \
  -p 8000:8000 \
  -v /var/run/docker.sock:/var/run/docker.sock \
  my-plugin:v1.0.0
```

### Dockerfile Example

```dockerfile
FROM python:3.11-slim

WORKDIR /app

# Install system dependencies
RUN apt-get update && apt-get install -y \
    curl \
    && rm -rf /var/lib/apt/lists/*

# Install Python dependencies
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# Copy application
COPY . .

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD curl -f http://localhost:8000/health || exit 1

# Run application
CMD ["uvicorn", "main:app", "--host", "0.0.0.0", "--port", "8000"]
```

## Next Steps

- Explore the
  [interLink Kubernetes Plugin](https://github.com/interlink-hq/interlink-kubernetes-plugin)
  for a production example
- Check out the
  [Plugin SDK documentation](https://github.com/interlink-hq/interlink-plugin-sdk)
  for API details
- Review the [monitoring guide](./05-monitoring.md) to add observability to your
  plugin
- Study the [API reference](./03-api-reference.mdx) for detailed endpoint
  specifications
- Join the interLink community for support and contributions
