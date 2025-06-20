# InterLink CRI Implementation Usage Guide

This document provides comprehensive guidance on using the interLink CRI (Container Runtime Interface) implementation as a kubelet container runtime.

## Overview

The interLink CRI implementation bridges the Kubernetes CRI with interLink's remote execution capabilities, allowing kubelet to manage containers through interLink's plugin architecture. This enables running containers on remote resources (HPC clusters, cloud providers, etc.) while maintaining full CRI compatibility.

## Architecture

The interLink CRI implementation provides a standalone binary that acts as a bridge between kubelet and the interLink API server:

```
┌─────────────┐   CRI gRPC    ┌─────────────────┐   HTTP/REST     ┌─────────────────┐   Plugin API   ┌─────────────┐
│   Kubelet   │ ─────────────▶│ InterLink CRI   │ ───────────────▶│ InterLink API   │ ──────────────▶│ InterLink   │
│             │   (socket)    │ Binary          │ (AuthN/AuthZ)   │ Server          │                │ Plugin      │
└─────────────┘               └─────────────────┘                 └─────────────────┘                └─────────────┘
                                       │                                   │
                                       ▼                                   ▼
                              ┌─────────────────┐                 ┌─────────────────┐
                              │ Pod Status      │                 │ Remote Resource │
                              │ Tracking        │                 │ (HPC/Cloud)     │
                              └─────────────────┘                 └─────────────────┘
```

**Key Benefits of Standalone Architecture:**
- Clean separation between CRI and interLink API server
- Independent deployment and scaling
- Standard CRI binary pattern (like containerd, cri-o)
- Flexible authentication/authorization options
- Can be used alongside existing container runtimes

## Prerequisites

1. **InterLink Server**: A running interLink API server with plugin configured
2. **Go 1.21+**: For building the CRI implementation
3. **Kubernetes Cluster**: With kubelet configuration access
4. **Network Connectivity**: Between kubelet and interLink CRI socket

## Installation and Setup

### 1. Build InterLink CRI Binary

Build the standalone CRI binary:

```bash
# Clone the interLink repository
git clone https://github.com/interlink-hq/interlink.git
cd interlink

# Build standalone CRI binary
go build -o bin/interlink-cri cmd/interlink/cri.go

# Or use make target (if available)
make cri
```

The CRI binary is completely independent from the interLink API server and can be deployed separately.

### 2. Configure InterLink CRI Binary

Create a CRI-specific configuration file (`InterLinkCRIConfig.yaml`):

```yaml
# InterLink API Server connection
InterlinkAddress: "https://interlink-api.example.com"  # InterLink API server endpoint
Interlinkport: "8080"

# CRI Socket configuration
CRI:
  SocketPath: "unix:///var/run/interlink/cri.sock"
  RuntimeHandler: "interlink"

# Logging configuration
VerboseLogging: true
ErrorsOnlyLogging: false

# TLS Configuration for mTLS authentication with InterLink API server
TLS:
  Enabled: true
  CertFile: "/etc/interlink/cri/tls/client.crt"    # Client certificate for mTLS
  KeyFile: "/etc/interlink/cri/tls/client.key"     # Client private key
  CACertFile: "/etc/interlink/cri/tls/ca.crt"      # CA certificate for server verification

# Local data for pod tracking
DataRootFolder: "/var/lib/interlink-cri"
```

**Note**: This configuration is for the CRI binary to connect to an existing interLink API server. The API server itself has its own separate configuration.

### 3. Configure Authentication

The CRI implementation supports OAuth token authentication and mTLS:

#### OAuth Token Authentication

For OAuth token authentication, the CRI implementation uses OIDC tokens following the same pattern as virtual-kubelet. Set the token file path via environment variable:

```bash
export VK_TOKEN_FILE="/etc/interlink/tokens/oidc.token"
```

Refer to the Virtual Kubelet OAuth configuration guide for complete OIDC token setup instructions.

**Note**: OIDC tokens are only required when using OAuth authentication. If using mTLS certificate authentication instead, token setup can be skipped.

#### mTLS Certificate Authentication

Generate client certificates for mutual TLS authentication:

```bash
# Generate client certificate (example with openssl)
openssl genrsa -out client.key 2048
openssl req -new -key client.key -out client.csr -subj "/CN=interlink-cri"
openssl x509 -req -in client.csr -CA ca.crt -CAkey ca.key -CAcreateserial -out client.crt -days 365
```

### 4. Start InterLink CRI Binary

```bash
# Start the standalone CRI binary
./bin/interlink-cri --config InterLinkCRIConfig.yaml

# Or run as a systemd service
sudo systemctl start interlink-cri
```

The CRI server will be available at the configured socket path (default: `unix:///var/run/interlink/cri.sock`).

#### Systemd Service Configuration

Create a systemd service file (`/etc/systemd/system/interlink-cri.service`):

```ini
[Unit]
Description=InterLink CRI Runtime
Documentation=https://interlink.readthedocs.io/
After=network.target
Wants=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/interlink-cri --config /etc/interlink/cri/config.yaml
Restart=always
RestartSec=5
KillMode=mixed
User=root
Group=root

# Resource limits
LimitNOFILE=1048576
LimitNPROC=1048576
LimitCORE=infinity

[Install]
WantedBy=multi-user.target
```

Enable and start the service:

```bash
sudo systemctl daemon-reload
sudo systemctl enable interlink-cri
sudo systemctl start interlink-cri
```

## Kubelet Configuration

### 1. Configure Kubelet for Remote CRI (Primary Runtime)

For using interLink CRI as the primary container runtime, create or modify the kubelet configuration file (`/etc/kubernetes/kubelet/config.yaml`):

```yaml
apiVersion: kubelet.config.k8s.io/v1beta1
kind: KubeletConfiguration

# CRI Configuration - points to InterLink CRI binary socket
containerRuntimeEndpoint: "unix:///var/run/interlink/cri.sock"
imageServiceEndpoint: "unix:///var/run/interlink/cri.sock"

# Runtime configuration
containerRuntime: "remote"
runtimeRequestTimeout: "15m"

# Node configuration
staticPodPath: "/etc/kubernetes/manifests"
clusterDomain: "cluster.local"
clusterDNS:
  - "10.96.0.10"

# Authentication
authentication:
  anonymous:
    enabled: false
  webhook:
    enabled: true

# Authorization
authorization:
  mode: Webhook

# Feature gates (if needed)
featureGates:
  CRIContainerLogRotation: true
```

### 2. Configure Multiple Container Runtimes (RuntimeClass)

For scenarios where interLink CRI is an additional runtime alongside the default container runtime (e.g., containerd), configure RuntimeClass:

```yaml
apiVersion: kubelet.config.k8s.io/v1beta1
kind: KubeletConfiguration

# Primary CRI (e.g., containerd)
containerRuntimeEndpoint: "unix:///run/containerd/containerd.sock"
imageServiceEndpoint: "unix:///run/containerd/containerd.sock"

# Runtime configuration
containerRuntime: "remote"
runtimeRequestTimeout: "15m"

# Enable RuntimeClass feature
featureGates:
  RuntimeClass: true
```

Create a RuntimeClass for interLink:

```yaml
apiVersion: node.k8s.io/v1
kind: RuntimeClass
metadata:
  name: interlink
handler: interlink
overhead:
  podFixed:
    memory: "128Mi"
    cpu: "100m"
scheduling:
  nodeClassification:
    tolerations:
    - key: "interlink.io/no-schedule"
      operator: "Exists"
      effect: "NoSchedule"
```

Configure the interLink CRI to register as a runtime handler:

```yaml
# In InterLinkConfig.yaml
CRI:
  Enabled: true
  SocketPath: "unix:///var/run/interlink/interlink.sock"
  RuntimeHandler: "interlink"
```

### 3. Start Kubelet with CRI Configuration

```bash
# Start kubelet with the configuration
kubelet --config=/etc/kubernetes/kubelet/config.yaml \
        --kubeconfig=/etc/kubernetes/kubelet/kubeconfig \
        --container-runtime-endpoint=unix:///var/run/interlink/cri.sock \
        --v=2
```

## Usage Examples

### 1. Deploy a Simple Pod (Primary Runtime)

Create a test pod for primary runtime (`test-pod.yaml`):

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: test-pod
  namespace: default
spec:
  containers:
  - name: test-container
    image: nginx:latest
    ports:
    - containerPort: 80
    resources:
      requests:
        memory: "128Mi"
        cpu: "100m"
      limits:
        memory: "256Mi"
        cpu: "200m"
    env:
    - name: ENV_VAR
      value: "test-value"
```

### 2. Deploy a Pod with Specific RuntimeClass

For multi-runtime setups, specify the RuntimeClass to use interLink CRI (`test-pod-interlink.yaml`):

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: test-pod-interlink
  namespace: default
spec:
  runtimeClassName: interlink  # Use interLink CRI runtime
  containers:
  - name: hpc-workload
    image: tensorflow/tensorflow:latest-gpu
    command: ["python3", "/app/train.py"]
    resources:
      requests:
        memory: "2Gi"
        cpu: "4"
        nvidia.com/gpu: "1"
      limits:
        memory: "8Gi"
        cpu: "8"
        nvidia.com/gpu: "2"
    env:
    - name: CUDA_VISIBLE_DEVICES
      value: "0,1"
    volumeMounts:
    - name: data-volume
      mountPath: /data
  volumes:
  - name: data-volume
    persistentVolumeClaim:
      claimName: hpc-data-pvc
  tolerations:
  - key: "interlink.io/no-schedule"
    operator: "Exists"
    effect: "NoSchedule"
```

### 3. Deploy Standard Pod (Default Runtime)

Regular pods will use the default runtime (e.g., containerd) (`test-pod-default.yaml`):

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: test-pod-default
  namespace: default
spec:
  # No runtimeClassName specified - uses default runtime
  containers:
  - name: web-server
    image: nginx:latest
    ports:
    - containerPort: 80
    resources:
      requests:
        memory: "64Mi"
        cpu: "50m"
      limits:
        memory: "128Mi"
        cpu: "100m"
```

Deploy the pods:

```bash
# Deploy primary runtime pod
kubectl apply -f test-pod.yaml

# Deploy interLink runtime pod (multi-runtime scenario)
kubectl apply -f test-pod-interlink.yaml

# Deploy default runtime pod (multi-runtime scenario)
kubectl apply -f test-pod-default.yaml
```

### 4. Monitor Pod Status

```bash
# Check pod status
kubectl get pods test-pod -o wide

# Get detailed pod information
kubectl describe pod test-pod

# Check container logs
kubectl logs test-pod
```

### 5. Interactive Container Access

```bash
# Execute commands in the container
kubectl exec -it test-pod -- /bin/bash

# Run specific commands
kubectl exec test-pod -- nginx -v
```

## Configuration Options

### CRI Binary Configuration

The standalone CRI binary configuration file supports the following options:

```yaml
# InterLink API Server connection
InterlinkAddress: "https://interlink-api.example.com"
Interlinkport: "8080"

# CRI Socket configuration
CRI:
  SocketPath: "unix:///var/run/interlink/cri.sock"  # Configurable socket path
  RuntimeHandler: "interlink"                        # Runtime handler name
  RequestTimeout: "15m"                             # Timeout for requests

# Authentication
TLS:
  Enabled: true
  CertFile: "/etc/interlink/cri/tls/client.crt"
  KeyFile: "/etc/interlink/cri/tls/client.key"
  CACertFile: "/etc/interlink/cri/tls/ca.crt"

# Logging
VerboseLogging: true
ErrorsOnlyLogging: false

# Local storage for pod tracking
DataRootFolder: "/var/lib/interlink-cri"
```

### Environment Variables

The CRI binary supports these environment variables:

```bash
# OAuth token file (alternative to mTLS)
export VK_TOKEN_FILE="/etc/interlink/tokens/oidc.token"

# Override config file location
export INTERLINK_CRI_CONFIG="/etc/interlink/cri/config.yaml"

# Enable debug logging
export INTERLINK_CRI_DEBUG="true"
```

## Troubleshooting

### Common Issues

#### 1. CRI Socket Connection Failed

**Error**: `Failed to connect to CRI socket`

**Solution**:
```bash
# Check if interLink CRI binary is running
ps aux | grep interlink-cri
systemctl status interlink-cri

# Check socket permissions and existence
ls -la /var/run/interlink/cri.sock

# Verify socket is listening
ss -xlp | grep cri.sock

# Check CRI binary logs
journalctl -u interlink-cri -f
```

#### 2. Container Creation Failed

**Error**: `Failed to create container`

**Check**:
1. InterLink API server connectivity
2. Authentication (OIDC token or mTLS)
3. Plugin connectivity from API server
4. Container resource requirements

```bash
# Check CRI binary logs
journalctl -u interlink-cri -f

# Check InterLink API server connectivity
curl -k https://interlink-api.example.com:8080/pinglink

# Test authentication
curl -k -H "Authorization: Bearer $(cat /etc/interlink/tokens/oidc.token)" \
     https://interlink-api.example.com:8080/status

# Check API server logs (if accessible)
journalctl -u interlink-api -f
```

#### 3. Pod Status Not Updated

**Error**: Pods stuck in `Pending` state

**Debug**:
```bash
# Check CRI container status through CRI binary
crictl --runtime-endpoint unix:///var/run/interlink/cri.sock ps

# Check InterLink API server pod tracking
curl -k https://interlink-api.example.com:8080/status

# Check CRI binary internal tracking
journalctl -u interlink-cri --since "5 minutes ago"
```

### Debugging Commands

```bash
# List CRI containers via standalone CRI binary
crictl --runtime-endpoint unix:///var/run/interlink/cri.sock ps -a

# Inspect container
crictl --runtime-endpoint unix:///var/run/interlink/cri.sock inspect <container-id>

# Check container logs
crictl --runtime-endpoint unix:///var/run/interlink/cri.sock logs <container-id>

# List pod sandboxes
crictl --runtime-endpoint unix:///var/run/interlink/cri.sock pods

# Test CRI binary directly
curl -X POST --unix-socket /var/run/interlink/cri.sock \
     -H "Content-Type: application/json" \
     http://localhost/healthz
```

## Monitoring and Observability

### Metrics

The CRI implementation provides integration with interLink's observability:

- **Container Lifecycle Metrics**: Creation, start, stop, removal
- **Pod Status Tracking**: State transitions and health
- **Request Tracing**: End-to-end request tracking through OpenTelemetry

### Logging

Enable detailed logging by setting:

```yaml
VerboseLogging: true
```

Log locations:
- **InterLink CRI**: Container lifecycle events
- **InterLink API**: HTTP requests and responses
- **Plugin**: Remote execution logs

## Advanced Configuration

### Custom Resource Mapping

Customize how CRI resource requirements map to interLink pods:

```go
// In convertToInterLinkPod function
if containerConfig.Linux != nil && containerConfig.Linux.Resources != nil {
    resources := v1.ResourceRequirements{}
    
    // Custom CPU mapping
    if containerConfig.Linux.Resources.CpuQuota > 0 {
        cpuLimit := containerConfig.Linux.Resources.CpuQuota / 1000
        resources.Limits = v1.ResourceList{
            v1.ResourceCPU: *resource.NewMilliQuantity(cpuLimit, resource.DecimalSI),
        }
    }
    
    // Custom memory mapping
    if containerConfig.Linux.Resources.MemoryLimitInBytes > 0 {
        if resources.Limits == nil {
            resources.Limits = v1.ResourceList{}
        }
        resources.Limits[v1.ResourceMemory] = *resource.NewQuantity(
            containerConfig.Linux.Resources.MemoryLimitInBytes, 
            resource.BinarySI,
        )
    }
    
    pod.Spec.Containers[0].Resources = resources
}
```

### Plugin Integration

Configure specific plugin behavior through environment variables or configuration:

```yaml
# Plugin-specific environment variables
PluginConfig:
  SLURM_PARTITION: "gpu"
  SLURM_QOS: "high"
  DOCKER_REGISTRY: "registry.example.com"
```

## Best Practices

### 1. Resource Management

- Set appropriate resource requests and limits
- Monitor resource usage through interLink metrics
- Use resource quotas at namespace level

### 2. Network Configuration

- Ensure proper network policies for container communication
- Configure DNS resolution for service discovery
- Use appropriate service mesh integration if needed

### 3. Security

- Enable TLS for interLink communication
- Use proper RBAC for kubelet service account
- Secure the CRI socket with appropriate permissions

### 4. Monitoring

- Set up monitoring for both kubelet and interLink
- Monitor container lifecycle metrics
- Track resource utilization on remote resources

## Migration Guide

### From Docker to InterLink CRI

1. **Backup Current Configuration**:
   ```bash
   cp /etc/kubernetes/kubelet/config.yaml /etc/kubernetes/kubelet/config.yaml.bak
   ```

2. **Update Kubelet Configuration**:
   ```yaml
   # Change from Docker to InterLink CRI
   containerRuntimeEndpoint: "unix:///tmp/kubelet_remote_1000.sock"
   ```

3. **Restart Services**:
   ```bash
   systemctl restart kubelet
   systemctl restart interlink
   ```

4. **Verify Migration**:
   ```bash
   kubectl get nodes
   kubectl get pods --all-namespaces
   ```

## Support and Community

- **Documentation**: [InterLink Documentation](https://interlink.readthedocs.io/)
- **Issues**: [GitHub Issues](https://github.com/interlink-hq/interlink/issues)
- **Discussions**: [GitHub Discussions](https://github.com/interlink-hq/interlink/discussions)
- **Slack**: [CNCF Slack #interlink](https://cloud-native.slack.com/channels/interlink)

## Contributing

To contribute to the CRI implementation:

1. Fork the repository
2. Create a feature branch
3. Make your changes following the existing patterns
4. Add tests for new functionality
5. Submit a pull request

For detailed contribution guidelines, see the project's contribution documentation.