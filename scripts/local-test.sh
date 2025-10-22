#!/bin/bash
# local-test.sh - Quick local integration test using existing K8s cluster
# Follows the pattern from example/debug.sh but with manual steps

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

echo "=== Running local interLink integration test ==="
echo "This script follows the pattern from example/debug.sh"
echo ""

# Check if KUBECONFIG is set
if [ -z "${KUBECONFIG}" ]; then
  echo "Using default kubeconfig: ~/.kube/config"
  export KUBECONFIG=~/.kube/config
fi

# Check kubectl connectivity
if ! kubectl get nodes &>/dev/null; then
  echo "ERROR: Cannot connect to Kubernetes cluster"
  echo "Please ensure KUBECONFIG is set correctly"
  exit 1
fi

echo "Connected to cluster:"
kubectl cluster-info
echo ""

# Use example configs as base
PLUGIN_CONFIG="${PROJECT_ROOT}/scripts/plugin-config.yaml"
INTERLINK_CONFIG="${PROJECT_ROOT}/scripts/interlink-config.yaml"
VK_CONFIG="${PROJECT_ROOT}/scripts/virtual-kubelet-config.yaml"

echo "=== Step 1: Build Docker Images ==="
echo ""
echo "Building interLink API Docker image..."
docker build -f "${PROJECT_ROOT}/docker/Dockerfile.interlink" \
  -t interlink:local "${PROJECT_ROOT}"

echo ""
echo "Building SLURM plugin from submodule..."
# Initialize submodule if not already done
if [ ! -f "${PROJECT_ROOT}/plugins/slurm/docker/Dockerfile" ]; then
  echo "Initializing plugins/slurm submodule..."
  cd "${PROJECT_ROOT}"
  git submodule update --init plugins/slurm
fi
docker build -f "${PROJECT_ROOT}/plugins/slurm/docker/Dockerfile" \
  -t interlink-slurm-plugin:local "${PROJECT_ROOT}/plugins/slurm"

echo ""
echo "✓ Docker images built successfully"
echo ""

echo "=== Step 2: Start SLURM Plugin Container ==="
echo ""
echo "Starting SLURM plugin container..."
docker run -d --name interlink-plugin \
  -p 4000:4000 --privileged \
  -v "${PLUGIN_CONFIG}:/etc/interlink/InterLinkConfig.yaml" \
  -e SHARED_FS=true \
  -e SLURMCONFIGPATH=/etc/interlink/InterLinkConfig.yaml \
  interlink-slurm-plugin:local

sleep 3
if ! docker ps | grep -q interlink-plugin; then
  echo "ERROR: Plugin container failed to start!"
  docker logs interlink-plugin
  exit 1
fi

echo "✓ SLURM plugin container started"
docker ps | grep interlink-plugin
echo ""

echo "=== Step 3: Start interLink API Container ==="
echo ""
echo "Starting interLink API container..."
docker run -d --name interlink-api \
  -p 3000:3000 \
  -v "${INTERLINK_CONFIG}:/etc/interlink/InterLinkConfig.yaml" \
  -e INTERLINKCONFIGPATH=/etc/interlink/InterLinkConfig.yaml \
  interlink:local

sleep 3
if ! docker ps | grep -q interlink-api; then
  echo "ERROR: interLink API container failed to start!"
  docker logs interlink-api
  exit 1
fi

# Test connectivity
echo "Testing interLink API connectivity..."
for i in {1..10}; do
  if curl -f http://localhost:3000/status 2>/dev/null; then
    echo "✓ interLink API is responding"
    break
  fi
  sleep 1
done

docker ps | grep interlink-api
echo ""

echo "=== Step 3.5: Create Virtual Kubelet Service Account ==="
echo ""
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

echo "✓ Service account created"
kubectl get sa virtual-kubelet -n default
echo ""

echo "=== Step 4: Start Virtual Kubelet ==="
echo ""
echo "To start Virtual Kubelet, run in another terminal:"
echo ""
echo "  ${SCRIPT_DIR}/start-vk.sh"
echo ""
echo "=== Step 5: Test with a Pod ==="
echo ""
echo "Once Virtual Kubelet is running, test with:"
echo ""
echo "  kubectl apply -f ${PROJECT_ROOT}/example/test_pod.yaml"
echo "  kubectl get pods -o wide"
echo "  kubectl get nodes"
echo "  kubectl logs <pod-name>"
echo ""
read -p "Press Enter to stop and cleanup..."

# Cleanup
echo ""
echo "=== Cleanup ==="
echo "Stopping containers..."
docker stop interlink-api interlink-plugin 2>/dev/null || true
docker rm interlink-api interlink-plugin 2>/dev/null || true

echo ""
echo "✓ Local test complete"
echo ""
echo "Note: This follows the same pattern as example/debug.sh but using:"
echo "  - SLURM plugin container (not Docker plugin binary)"
echo "  - interLink API container (not binary)"
echo "  - Virtual Kubelet with 'go run' (not 'dlv debug')"
