# InterLink CRI Quick Start Guide

This guide provides a quick setup and usage reference for the interLink CRI implementation.

## Quick Setup (5 minutes)

### 1. Build and Start InterLink CRI

```bash
# Build
make interlink

# Start with default configuration
./bin/interlink &

# CRI socket will be available at: unix:///tmp/kubelet_remote_1000.sock
```

### 2. Configure Kubelet

```bash
# Minimal kubelet configuration
kubelet --container-runtime=remote \
        --container-runtime-endpoint=unix:///tmp/kubelet_remote_1000.sock \
        --image-service-endpoint=unix:///tmp/kubelet_remote_1000.sock \
        --kubeconfig=/etc/kubernetes/kubelet.conf
```

### 3. Test with Simple Pod

```yaml
# save as test-pod.yaml
apiVersion: v1
kind: Pod
metadata:
  name: hello-interlink
spec:
  containers:
  - name: hello
    image: hello-world
```

```bash
kubectl apply -f test-pod.yaml
kubectl get pods -w
```

## Common Commands

### CRI Operations

```bash
# List containers
crictl --runtime-endpoint unix:///tmp/kubelet_remote_1000.sock ps

# List pod sandboxes  
crictl --runtime-endpoint unix:///tmp/kubelet_remote_1000.sock pods

# Get container logs
crictl --runtime-endpoint unix:///tmp/kubelet_remote_1000.sock logs <container-id>

# Execute in container
crictl --runtime-endpoint unix:///tmp/kubelet_remote_1000.sock exec -it <container-id> /bin/sh
```

### InterLink Status

```bash
# Check interLink health
curl -X POST http://localhost:8080/pinglink

# Get pod statuses
curl -X GET http://localhost:8080/status

# Check interLink logs
journalctl -u interlink -f
```

### Kubernetes Operations

```bash
# Standard kubectl commands work normally
kubectl get pods
kubectl describe pod <pod-name>
kubectl logs <pod-name>
kubectl exec -it <pod-name> -- /bin/bash
kubectl delete pod <pod-name>
```

## Configuration Templates

### Minimal InterLinkConfig.yaml

```yaml
InterlinkAddress: "http://localhost"
Interlinkport: "8080"
SidecarURL: "http://localhost"
Sidecarport: "4000"
VerboseLogging: true
```

### Production Kubelet Config

```yaml
apiVersion: kubelet.config.k8s.io/v1beta1
kind: KubeletConfiguration
containerRuntimeEndpoint: "unix:///tmp/kubelet_remote_1000.sock"
imageServiceEndpoint: "unix:///tmp/kubelet_remote_1000.sock"
containerRuntime: "remote"
runtimeRequestTimeout: "15m"
clusterDomain: "cluster.local"
clusterDNS: ["10.96.0.10"]
authentication:
  webhook:
    enabled: true
authorization:
  mode: Webhook
```

## Troubleshooting Quick Fixes

### Issue: CRI socket not found
```bash
# Check if interLink is running
ps aux | grep interlink

# Check socket exists
ls -la /tmp/kubelet_remote_1000.sock

# Restart interLink
pkill interlink
./bin/interlink &
```

### Issue: Pod stuck in Pending
```bash
# Check container creation
crictl --runtime-endpoint unix:///tmp/kubelet_remote_1000.sock ps -a

# Check interLink plugin connectivity
curl -X POST http://localhost:4000/status

# Check kubelet logs
journalctl -u kubelet -f
```

### Issue: Container logs not available
```bash
# Direct CRI log access
crictl --runtime-endpoint unix:///tmp/kubelet_remote_1000.sock logs <container-id>

# Check interLink log endpoint
curl -X GET http://localhost:8080/getLogs
```

## Example Workloads

### Batch Job

```yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: pi-calculation
spec:
  template:
    spec:
      containers:
      - name: pi
        image: perl:5.32
        command: ["perl", "-Mbignum=bpi", "-wle", "print bpi(2000)"]
        resources:
          requests:
            cpu: "100m"
            memory: "128Mi"
          limits:
            cpu: "500m"
            memory: "256Mi"
      restartPolicy: Never
  backoffLimit: 4
```

### Web Service

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: nginx-server
  labels:
    app: nginx
spec:
  containers:
  - name: nginx
    image: nginx:alpine
    ports:
    - containerPort: 80
    env:
    - name: NGINX_HOST
      value: "localhost"
    resources:
      requests:
        cpu: "100m"
        memory: "128Mi"
---
apiVersion: v1
kind: Service
metadata:
  name: nginx-service
spec:
  selector:
    app: nginx
  ports:
  - port: 80
    targetPort: 80
  type: ClusterIP
```

### Init Container Example

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: init-demo
spec:
  initContainers:
  - name: init-setup
    image: busybox:1.35
    command: ['sh', '-c', 'echo "Setup complete" > /shared/status']
    volumeMounts:
    - name: shared-data
      mountPath: /shared
  containers:
  - name: main-app
    image: busybox:1.35
    command: ['sh', '-c', 'cat /shared/status && sleep 3600']
    volumeMounts:
    - name: shared-data
      mountPath: /shared
  volumes:
  - name: shared-data
    emptyDir: {}
```

## Performance Tips

1. **Resource Requests**: Always set CPU/memory requests for better scheduling
2. **Image Pulls**: Use image pull policies appropriate for your environment
3. **Health Checks**: Implement proper readiness and liveness probes
4. **Graceful Shutdown**: Set appropriate termination grace periods

## Environment Variables

Key environment variables for the CRI implementation:

```bash
# Enable debug logging
export INTERLINK_VERBOSE=true

# Custom CRI socket path
export CRI_SOCKET_PATH=/var/run/interlink/cri.sock

# Plugin timeout
export PLUGIN_TIMEOUT=300s

# OpenTelemetry tracing
export ENABLE_TRACING=1
export OTEL_SERVICE_NAME=interlink-cri
```

## Next Steps

After getting the basic setup working:

1. **Configure your plugin**: Set up SLURM, Docker, or custom plugin
2. **Security**: Enable TLS and proper authentication
3. **Monitoring**: Set up metrics and observability
4. **Production**: Configure resource quotas and limits
5. **Scale**: Deploy across multiple nodes

For detailed information, see the full [CRI Usage Guide](CRI_USAGE.md).