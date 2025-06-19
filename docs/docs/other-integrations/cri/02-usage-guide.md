# InterLink CRI Implementation Usage Guide

This document provides comprehensive guidance on using the interLink CRI (Container Runtime Interface) implementation as a kubelet container runtime.

## Overview

The interLink CRI implementation bridges the Kubernetes CRI with interLink's remote execution capabilities, allowing kubelet to manage containers through interLink's plugin architecture. This enables running containers on remote resources (HPC clusters, cloud providers, etc.) while maintaining full CRI compatibility.

## Architecture

```
┌─────────────┐    CRI gRPC     ┌─────────────────┐    HTTP/REST    ┌─────────────┐
│   Kubelet   │ ──────────────▶ │ InterLink CRI   │ ──────────────▶ │ InterLink   │
│             │                 │ Implementation  │                 │ Plugin      │
└─────────────┘                 └─────────────────┘                 └─────────────┘
                                         │
                                         ▼
                                ┌─────────────────┐
                                │ Pod Status      │
                                │ Tracking        │
                                └─────────────────┘
```

## Prerequisites

1. **InterLink Server**: A running interLink API server with plugin configured
2. **Go 1.21+**: For building the CRI implementation
3. **Kubernetes Cluster**: With kubelet configuration access
4. **Network Connectivity**: Between kubelet and interLink CRI socket

## Installation and Setup

### 1. Build InterLink with CRI Support

```bash
# Clone the interLink repository
git clone https://github.com/interlink-hq/interlink.git
cd interlink

# Build interLink with CRI implementation
make interlink

# Or build directly
go build -o bin/interlink cmd/interlink/main.go cmd/interlink/cri.go
```

### 2. Configure InterLink

Create an InterLink configuration file (`InterLinkConfig.yaml`):

```yaml
# InterLink API configuration
InterlinkAddress: "unix:///var/run/interlink/interlink.sock"
Interlinkport: "8080"
SidecarURL: "http://localhost"
Sidecarport: "4000"

# CRI-specific configuration
VerboseLogging: true
ErrorsOnlyLogging: false

# TLS Configuration (optional)
TLS:
  Enabled: true
  CertFile: "/etc/interlink/tls/server.crt"
  KeyFile: "/etc/interlink/tls/server.key"
  CACertFile: "/etc/interlink/tls/ca.crt"

# Plugin configuration
DataRootFolder: "/tmp/interlink"
```

### 3. Start InterLink CRI Server

```bash
# Start interLink with CRI support
./bin/interlink --config InterLinkConfig.yaml
```

The CRI server will be available at `unix:///tmp/kubelet_remote_1000.sock` by default.

## Kubelet Configuration

### 1. Configure Kubelet for Remote CRI

Create or modify the kubelet configuration file (`/etc/kubernetes/kubelet/config.yaml`):

```yaml
apiVersion: kubelet.config.k8s.io/v1beta1
kind: KubeletConfiguration

# CRI Configuration
containerRuntimeEndpoint: "unix:///tmp/kubelet_remote_1000.sock"
imageServiceEndpoint: "unix:///tmp/kubelet_remote_1000.sock"

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

### 2. Start Kubelet with CRI Configuration

```bash
# Start kubelet with the configuration
kubelet --config=/etc/kubernetes/kubelet/config.yaml \
        --kubeconfig=/etc/kubernetes/kubelet/kubeconfig \
        --container-runtime-endpoint=unix:///tmp/kubelet_remote_1000.sock \
        --v=2
```

## Usage Examples

### 1. Deploy a Simple Pod

Create a test pod (`test-pod.yaml`):

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

Deploy the pod:

```bash
kubectl apply -f test-pod.yaml
```

### 2. Monitor Pod Status

```bash
# Check pod status
kubectl get pods test-pod -o wide

# Get detailed pod information
kubectl describe pod test-pod

# Check container logs
kubectl logs test-pod
```

### 3. Interactive Container Access

```bash
# Execute commands in the container
kubectl exec -it test-pod -- /bin/bash

# Run specific commands
kubectl exec test-pod -- nginx -v
```

## Configuration Options

### CRI Socket Configuration

The CRI socket path can be configured in the interLink main function:

```go
// In cmd/interlink/main.go
interlinkRuntime := NewFakeRemoteRuntime(&interLinkAPIs)
err = interlinkRuntime.Start("unix:///var/run/interlink/cri.sock")
```

### InterLink Handler Configuration

The CRI implementation integrates with interLink through the `InterLinkHandler`:

```go
interLinkAPIs := api.InterLinkHandler{
    Config:          interLinkConfig,
    Ctx:             ctx,
    SidecarEndpoint: sidecarEndpoint,
    ClientHTTP:      clientHTTP,
}
```

## Troubleshooting

### Common Issues

#### 1. CRI Socket Connection Failed

**Error**: `Failed to connect to CRI socket`

**Solution**:
```bash
# Check if interLink CRI is running
ps aux | grep interlink

# Check socket permissions
ls -la /tmp/kubelet_remote_1000.sock

# Verify socket is listening
ss -xlp | grep kubelet_remote
```

#### 2. Container Creation Failed

**Error**: `Failed to create container`

**Check**:
1. InterLink plugin connectivity
2. Pod status tracking
3. Container resource requirements

```bash
# Check interLink logs
journalctl -u interlink -f

# Verify plugin status
curl -X POST http://localhost:8080/pinglink
```

#### 3. Pod Status Not Updated

**Error**: Pods stuck in `Pending` state

**Debug**:
```bash
# Check CRI container status
crictl --runtime-endpoint unix:///tmp/kubelet_remote_1000.sock ps

# Check interLink pod tracking
curl -X GET http://localhost:8080/status
```

### Debugging Commands

```bash
# List CRI containers
crictl --runtime-endpoint unix:///tmp/kubelet_remote_1000.sock ps -a

# Inspect container
crictl --runtime-endpoint unix:///tmp/kubelet_remote_1000.sock inspect <container-id>

# Check container logs
crictl --runtime-endpoint unix:///tmp/kubelet_remote_1000.sock logs <container-id>

# List pod sandboxes
crictl --runtime-endpoint unix:///tmp/kubelet_remote_1000.sock pods
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

For detailed contribution guidelines, see [CONTRIBUTING.md](../CONTRIBUTING.md).