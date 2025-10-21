#!/bin/bash
# k3s-test-setup.sh - Setup ephemeral K3s cluster for interLink integration tests
# Follows the pattern from example/debug.sh

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
TEST_DIR="/tmp/interlink-test-$$"

echo "=== Setting up ephemeral K3s cluster for interLink integration tests ==="
echo "Test directory: ${TEST_DIR}"

# Create test directory
mkdir -p "${TEST_DIR}"
export TEST_DIR

# Save test directory for other scripts
echo "${TEST_DIR}" > /tmp/interlink-test-dir.txt

# Download K3s if not already installed
if ! command -v k3s &> /dev/null; then
    echo "Downloading K3s..."
    curl -sfL https://get.k3s.io | INSTALL_K3S_VERSION=v1.31.4+k3s1 sh -
else
    echo "K3s already installed: $(k3s --version)"
fi

# Start K3s
echo "Starting K3s cluster..."
sudo k3s server --disable traefik --write-kubeconfig-mode=644 > "${TEST_DIR}/k3s.log" 2>&1 &
echo $! > "${TEST_DIR}/k3s.pid"

# Wait for K3s to be ready
echo "Waiting for K3s to be ready..."
export KUBECONFIG=/etc/rancher/k3s/k3s.yaml
for i in {1..60}; do
    if kubectl get nodes &>/dev/null; then
        echo "K3s cluster is ready!"
        break
    fi
    echo "Waiting for K3s... ($i/60)"
    sleep 2
done

kubectl wait --for=condition=Ready nodes --all --timeout=300s
kubectl get nodes

# Create Virtual Kubelet service account
echo "Creating service account and RBAC for Virtual Kubelet..."
kubectl apply -f - <<EOF
apiVersion: v1
kind: ServiceAccount
metadata:
  name: virtual-kubelet
  namespace: default
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: virtual-kubelet
rules:
- apiGroups:
  - "coordination.k8s.io"
  resources:
  - leases
  verbs:
  - update
  - create
  - get
  - list
  - watch
  - patch
- apiGroups:
  - ""
  resources:
  - configmaps
  - secrets
  - services
  - serviceaccounts
  - namespaces
  verbs:
  - get
  - list
  - watch
- apiGroups: [""]
  resources: ["serviceaccounts/token"]
  verbs:
  - create
  - get
  - list
- apiGroups:
  - ""
  resources:
  - pods
  verbs:
  - delete
  - get
  - list
  - watch
  - patch
- apiGroups:
  - ""
  resources:
  - nodes
  verbs:
  - create
  - get
- apiGroups:
  - ""
  resources:
  - nodes/status
  verbs:
  - update
  - patch
- apiGroups:
  - ""
  resources:
  - pods/status
  verbs:
  - update
  - patch
- apiGroups:
  - ""
  resources:
  - events
  verbs:
  - create
  - patch
- apiGroups:
  - "certificates.k8s.io"
  resources:
  - certificatesigningrequests
  verbs:
  - create
  - get
  - list
  - watch
  - delete
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: virtual-kubelet
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: virtual-kubelet
subjects:
- kind: ServiceAccount
  name: virtual-kubelet
  namespace: default
EOF

echo "Waiting for service account to be ready..."
sleep 5
kubectl get sa virtual-kubelet -n default

# Build Docker images
echo "Building interLink API Docker image..."
docker build -f "${PROJECT_ROOT}/docker/Dockerfile.interlink" \
    -t interlink:ci-test "${PROJECT_ROOT}"

echo "Cloning SLURM plugin repository..."
git clone https://github.com/interlink-hq/interlink-slurm-plugin.git "${TEST_DIR}/plugin-src"

echo "Building SLURM plugin Docker image..."
docker build -t interlink-slurm-plugin:ci-test "${TEST_DIR}/plugin-src"

echo "Docker images built successfully"
docker images | grep -E "interlink|slurm"

# Create plugin configuration
echo "Creating plugin configuration..."
cat > "${TEST_DIR}/plugin-config.yaml" <<EOF
InterlinkURL: "http://localhost"
InterlinkPort: "3000"
SidecarURL: "http://0.0.0.0"
SidecarPort: "4000"
VerboseLogging: true
ErrorsOnlyLogging: false
DataRootFolder: "${TEST_DIR}/.interlink/"
ExportPodData: true
EOF

# Start SLURM plugin container (following example/debug.sh pattern)
echo "Starting SLURM plugin container..."
docker run -d --name interlink-plugin \
    -p 4000:4000 --privileged \
    -v "${TEST_DIR}/plugin-config.yaml:/etc/interlink/InterLinkConfig.yaml" \
    -e SHARED_FS=true \
    -e SLURMCONFIGPATH=/etc/interlink/InterLinkConfig.yaml \
    interlink-slurm-plugin:ci-test

sleep 5
if ! docker ps | grep -q interlink-plugin; then
    echo "ERROR: Plugin container failed to start!"
    docker logs interlink-plugin
    exit 1
fi
echo "SLURM plugin container started successfully"
docker ps | grep interlink-plugin

# Create interLink configuration
echo "Creating interLink configuration..."
cat > "${TEST_DIR}/interlink-config.yaml" <<EOF
InterlinkAddress: "http://0.0.0.0"
InterlinkPort: "3000"
SidecarURL: "http://localhost"
SidecarPort: "4000"
VerboseLogging: true
ErrorsOnlyLogging: false
DataRootFolder: "${TEST_DIR}/.interlink"
EOF

# Start interLink API container (following example/debug.sh pattern)
echo "Starting interLink API container..."
docker run -d --name interlink-api \
    -p 3000:3000 \
    -v "${TEST_DIR}/interlink-config.yaml:/etc/interlink/InterLinkConfig.yaml" \
    -e INTERLINKCONFIGPATH=/etc/interlink/InterLinkConfig.yaml \
    interlink:ci-test

sleep 5
if ! docker ps | grep -q interlink-api; then
    echo "ERROR: interLink API container failed to start!"
    docker logs interlink-api
    exit 1
fi

# Test interLink connectivity
echo "Testing interLink API connectivity..."
for i in {1..10}; do
    if curl -f http://localhost:3000/status 2>/dev/null; then
        echo "interLink API is responding"
        break
    fi
    echo "Waiting for interLink API... ($i/10)"
    sleep 2
done
echo "interLink API container started successfully"
docker ps | grep interlink-api

# Start Virtual Kubelet on host (following example/debug.sh pattern but with 'go run' instead of 'dlv debug')
echo "Starting Virtual Kubelet on host..."
export NODENAME=virtual-kubelet
export KUBELET_PORT=10250
export KUBELET_URL=0.0.0.0

# Cross-platform IP detection
if command -v hostname &> /dev/null && hostname -I &> /dev/null 2>&1; then
    # Linux
    export POD_IP=$(hostname -I | awk '{print $1}')
else
    # macOS or other Unix
    export POD_IP=$(ifconfig | grep "inet " | grep -v 127.0.0.1 | head -1 | awk '{print $2}')
fi

export CONFIGPATH="${PROJECT_ROOT}/scripts/virtual-kubelet-config.yaml"

# Create token-based kubeconfig for Virtual Kubelet
echo "Creating token-based kubeconfig..."

# Get service account token
VK_TOKEN=$(kubectl create token virtual-kubelet -n default --duration=24h --kubeconfig=/etc/rancher/k3s/k3s.yaml)

# Get cluster info from current kubeconfig
K8S_SERVER=$(kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}' --kubeconfig=/etc/rancher/k3s/k3s.yaml)

# Check if certificate-authority-data exists, otherwise use certificate-authority file
K8S_CA_DATA=$(kubectl config view --minify --raw -o jsonpath='{.clusters[0].cluster.certificate-authority-data}' --kubeconfig=/etc/rancher/k3s/k3s.yaml)
K8S_CA_FILE=$(kubectl config view --minify --raw -o jsonpath='{.clusters[0].cluster.certificate-authority}' --kubeconfig=/etc/rancher/k3s/k3s.yaml)

if [ -n "$K8S_CA_DATA" ]; then
    # Use certificate-authority-data if available
    cat > "${TEST_DIR}/vk-kubeconfig.yaml" <<EOF
apiVersion: v1
kind: Config
clusters:
- name: default-cluster
  cluster:
    server: ${K8S_SERVER}
    certificate-authority-data: ${K8S_CA_DATA}
contexts:
- name: default-context
  context:
    cluster: default-cluster
    user: virtual-kubelet
    namespace: default
current-context: default-context
users:
- name: virtual-kubelet
  user:
    token: ${VK_TOKEN}
EOF
elif [ -n "$K8S_CA_FILE" ] && [ -f "$K8S_CA_FILE" ]; then
    # Use certificate-authority file and encode it (cross-platform base64)
    if base64 --help 2>&1 | grep -q -- '-w'; then
        K8S_CA_DATA=$(cat "$K8S_CA_FILE" | base64 -w 0)
    else
        K8S_CA_DATA=$(cat "$K8S_CA_FILE" | base64)
    fi
    cat > "${TEST_DIR}/vk-kubeconfig.yaml" <<EOF
apiVersion: v1
kind: Config
clusters:
- name: default-cluster
  cluster:
    server: ${K8S_SERVER}
    certificate-authority-data: ${K8S_CA_DATA}
contexts:
- name: default-context
  context:
    cluster: default-cluster
    user: virtual-kubelet
    namespace: default
current-context: default-context
users:
- name: virtual-kubelet
  user:
    token: ${VK_TOKEN}
EOF
else
    echo "ERROR: Could not find CA certificate"
    exit 1
fi

export KUBECONFIG="${TEST_DIR}/vk-kubeconfig.yaml"

echo "Virtual Kubelet environment:"
echo "  NODENAME: ${NODENAME}"
echo "  KUBELET_PORT: ${KUBELET_PORT}"
echo "  POD_IP: ${POD_IP}"
echo "  CONFIGPATH: ${CONFIGPATH}"
echo "  KUBECONFIG: ${KUBECONFIG} (token-based)"

cd "${PROJECT_ROOT}"
go run ./cmd/virtual-kubelet/main.go > "${TEST_DIR}/vk.log" 2>&1 &
echo $! > "${TEST_DIR}/vk.pid"

echo "Virtual Kubelet started (PID: $(cat ${TEST_DIR}/vk.pid))"

# Wait for VK to register with K8s
sleep 15

echo "Checking for virtual-kubelet node..."
kubectl get nodes

if ! kubectl get node virtual-kubelet 2>/dev/null; then
    echo "WARNING: Virtual Kubelet node not found yet"
    echo "Virtual Kubelet logs:"
    tail -50 "${TEST_DIR}/vk.log"
fi

# Approve CSRs
echo "Approving Certificate Signing Requests..."
sleep 5
kubectl get csr -o name | xargs -r kubectl certificate approve || true
kubectl get csr

echo ""
echo "=== K3s cluster setup complete ==="
echo "Test directory: ${TEST_DIR}"
echo "KUBECONFIG: /etc/rancher/k3s/k3s.yaml"
echo ""
echo "Component logs available at:"
echo "  - K3s: ${TEST_DIR}/k3s.log"
echo "  - SLURM plugin: docker logs interlink-plugin"
echo "  - interLink API: docker logs interlink-api"
echo "  - Virtual Kubelet: ${TEST_DIR}/vk.log"
echo ""
echo "Running components:"
echo "  - SLURM plugin container: interlink-plugin (port 4000)"
echo "  - interLink API container: interlink-api (port 3000)"
echo "  - Virtual Kubelet process: PID $(cat ${TEST_DIR}/vk.pid)"
echo ""
