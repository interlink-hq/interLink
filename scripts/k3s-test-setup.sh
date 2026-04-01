#!/bin/bash
# k3s-test-setup.sh - Set up ephemeral K3s cluster for integration testing
#
# Usage: ./scripts/k3s-test-setup.sh
# Requirements: sudo access (for K3s), Docker, Go 1.26+

set -eo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

echo "=== Setting up interLink integration test environment ==="
echo "Project root: ${PROJECT_ROOT}"

# Create or reuse test directory
# Allow caller to specify TEST_DIR (e.g., to coordinate with other scripts).
if [[ -n "${TEST_DIR:-}" ]]; then
  echo "Using existing TEST_DIR: ${TEST_DIR}"
else
  TEST_DIR=$(mktemp -d /tmp/interlink-test-XXXXXX)
  echo "Created TEST_DIR: ${TEST_DIR}"
fi

# Persist TEST_DIR so that k3s-test-run.sh and k3s-test-cleanup.sh can find it.
STATE_FILE="/tmp/interlink-test-dir.txt"
echo "${TEST_DIR}" > "${STATE_FILE}"
echo "State file: ${STATE_FILE}"
# ---------------------------------------------------------------------------
# Install and start K3s
# ---------------------------------------------------------------------------
echo ""
echo "=== Installing K3s ==="
K3S_VERSION="${K3S_VERSION:-v1.31.4+k3s1}"
echo "K3s version: ${K3S_VERSION}"

curl -sfL https://get.k3s.io | \
  INSTALL_K3S_VERSION="${K3S_VERSION}" sudo sh -s - --disable=traefik \
  2>&1 | tee "${TEST_DIR}/k3s-install.log"

# Make kubeconfig readable by the current user
sudo chmod 644 /etc/rancher/k3s/k3s.yaml
export KUBECONFIG=/etc/rancher/k3s/k3s.yaml

# Wait for K3s to be ready
echo "Waiting for K3s to be ready..."
# Previous behavior waited up to 30 * 5s = 150s
if ! kubectl wait --for=condition=Ready node --all --timeout=150s; then
  echo "ERROR: K3s did not become ready in time"
  # Best-effort: show node status if possible
  kubectl get nodes || true
  cat "${TEST_DIR}/k3s-install.log"
  exit 1
fi

echo "✓ K3s is ready!"
kubectl get nodes
# ---------------------------------------------------------------------------
# Build Docker images from source
# ---------------------------------------------------------------------------
echo ""
echo "=== Building Docker images ==="

echo "Building interLink API image..."
docker build -f "${PROJECT_ROOT}/docker/Dockerfile.interlink" \
  -t interlink:local "${PROJECT_ROOT}" \
  2>&1 | tee "${TEST_DIR}/build-interlink.log"
echo "✓ interLink API image built"

echo "Initializing SLURM plugin submodule..."
cd "${PROJECT_ROOT}"
git submodule update --init plugins/slurm

echo "Building SLURM plugin image..."
docker build -f "${PROJECT_ROOT}/plugins/slurm/docker/Dockerfile" \
  -t interlink-slurm-plugin:local "${PROJECT_ROOT}/plugins/slurm" \
  2>&1 | tee "${TEST_DIR}/build-plugin.log"
echo "✓ SLURM plugin image built"

# ---------------------------------------------------------------------------
# Create Docker network for inter-container communication
# ---------------------------------------------------------------------------
docker network create interlink-net 2>/dev/null || \
  echo "Docker network 'interlink-net' already exists, reusing."

# ---------------------------------------------------------------------------
# Generate runtime configs
# ---------------------------------------------------------------------------
mkdir -p "${TEST_DIR}/.interlink"

cat > "${TEST_DIR}/plugin-config.yaml" <<EOF
InterlinkURL: "http://interlink-api"
InterlinkPort: "3000"
SidecarURL: "http://0.0.0.0"
SidecarPort: "4000"
VerboseLogging: true
ErrorsOnlyLogging: false
DataRootFolder: "/tmp/.interlink"
ExportPodData: true
SbatchPath: "/usr/bin/sbatch"
ScancelPath: "/usr/bin/scancel"
SqueuePath: "/usr/bin/squeue"
CommandPrefix: ""
SingularityPrefix: ""
Namespace: "default"
Tsocks: false
BashPath: /bin/bash
EnableProbes: true
EOF

cat > "${TEST_DIR}/interlink-config.yaml" <<EOF
InterlinkAddress: "http://0.0.0.0"
InterlinkPort: "3000"
SidecarURL: "http://interlink-plugin"
SidecarPort: "4000"
VerboseLogging: true
ErrorsOnlyLogging: false
DataRootFolder: "/tmp/.interlink-api"
EOF

# ---------------------------------------------------------------------------
# Start SLURM plugin container
# ---------------------------------------------------------------------------
echo ""
echo "=== Starting SLURM plugin container ==="
docker run -d --name interlink-plugin \
  --network interlink-net \
  -p 4000:4000 \
  -v "${TEST_DIR}/plugin-config.yaml:/etc/interlink/InterLinkConfig.yaml:ro" \
  -e SHARED_FS=true \
  -e SLURMCONFIGPATH=/etc/interlink/InterLinkConfig.yaml \
  interlink-slurm-plugin:local

sleep 3
if ! docker ps --filter "name=interlink-plugin" --filter "status=running" | grep -q interlink-plugin; then
  echo "ERROR: SLURM plugin container failed to start"
  docker logs interlink-plugin 2>&1
  exit 1
fi
echo "✓ SLURM plugin container started"

# Stream plugin container logs to file in the background
docker logs -f interlink-plugin > "${TEST_DIR}/interlink-plugin.log" 2>&1 &
echo $! > "${TEST_DIR}/plugin-log.pid"
echo "  Plugin logs streaming to: ${TEST_DIR}/interlink-plugin.log"

# ---------------------------------------------------------------------------
# Start interLink API container
# ---------------------------------------------------------------------------
echo ""
echo "=== Starting interLink API container ==="
docker run -d --name interlink-api \
  --network interlink-net \
  -p 3000:3000 \
  -v "${TEST_DIR}/interlink-config.yaml:/etc/interlink/InterLinkConfig.yaml:ro" \
  -e INTERLINKCONFIGPATH=/etc/interlink/InterLinkConfig.yaml \
  interlink:local

sleep 3
if ! docker ps --filter "name=interlink-api" --filter "status=running" | grep -q interlink-api; then
  echo "ERROR: interLink API container failed to start"
  docker logs interlink-api 2>&1
  exit 1
fi

echo "Waiting for interLink API to respond..."
interlink_ready=0
for i in $(seq 1 20); do
  if curl -sf -X POST http://localhost:3000/pinglink >/dev/null 2>&1; then
    echo "✓ interLink API is ready"
    interlink_ready=1
    break
  fi
  echo "  Waiting... ($i/20)"
  sleep 3
done

if [ "${interlink_ready}" -ne 1 ]; then
  echo "ERROR: interLink API did not become ready within the expected time"
  docker logs interlink-api 2>&1 || true
  exit 1
fi
echo "✓ interLink API container started"

# Stream interLink API container logs to file in the background
docker logs -f interlink-api > "${TEST_DIR}/interlink-api.log" 2>&1 &
echo $! > "${TEST_DIR}/api-log.pid"
echo "  API logs streaming to: ${TEST_DIR}/interlink-api.log"

# ---------------------------------------------------------------------------
# Create Virtual Kubelet service account and RBAC
# ---------------------------------------------------------------------------
echo ""
echo "=== Creating Virtual Kubelet RBAC ==="
kubectl apply -f - <<'YAML'
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
- apiGroups: ["coordination.k8s.io"]
  resources: ["leases"]
  verbs: ["update", "create", "get", "list", "watch", "patch"]
- apiGroups: [""]
  resources: ["configmaps", "secrets", "services", "serviceaccounts", "namespaces"]
  verbs: ["get", "list", "watch"]
- apiGroups: [""]
  resources: ["serviceaccounts/token"]
  verbs: ["create", "get", "list"]
- apiGroups: [""]
  resources: ["pods"]
  verbs: ["delete", "get", "list", "watch", "patch"]
- apiGroups: [""]
  resources: ["nodes"]
  verbs: ["create", "get"]
- apiGroups: [""]
  resources: ["nodes/status"]
  verbs: ["update", "patch"]
- apiGroups: [""]
  resources: ["pods/status"]
  verbs: ["update", "patch"]
- apiGroups: [""]
  resources: ["events"]
  verbs: ["create", "patch"]
- apiGroups: ["certificates.k8s.io"]
  resources: ["certificatesigningrequests"]
  verbs: ["create", "get", "list", "watch", "delete"]
- apiGroups: ["certificates.k8s.io"]
  resources: ["certificatesigningrequests/approval"]
  verbs: ["update", "patch"]
- apiGroups: ["certificates.k8s.io"]
  resources: ["signers"]
  resourceNames: ["kubernetes.io/kubelet-serving"]
  verbs: ["approve"]
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
YAML
echo "✓ Service account and RBAC created"

# ---------------------------------------------------------------------------
# Build Virtual Kubelet binary
# ---------------------------------------------------------------------------
echo ""
echo "=== Building Virtual Kubelet binary ==="
cd "${PROJECT_ROOT}"
CGO_ENABLED=0 go build -o "${TEST_DIR}/vk" ./cmd/virtual-kubelet
echo "✓ Virtual Kubelet binary built"

# ---------------------------------------------------------------------------
# Create VK kubeconfig using service account token
# ---------------------------------------------------------------------------
echo "Creating VK kubeconfig..."
VK_TOKEN=$(kubectl create token virtual-kubelet -n default --duration=24h)
K8S_SERVER=$(kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}')
K8S_CA_DATA=$(kubectl config view --minify --raw -o jsonpath='{.clusters[0].cluster.certificate-authority-data}')

if [ -z "${K8S_CA_DATA}" ]; then
  K8S_CA_FILE=$(kubectl config view --minify --raw -o jsonpath='{.clusters[0].cluster.certificate-authority}')
  if [ -n "${K8S_CA_FILE}" ] && [ -f "${K8S_CA_FILE}" ]; then
    K8S_CA_DATA=$(base64 -w 0 < "${K8S_CA_FILE}" 2>/dev/null || base64 < "${K8S_CA_FILE}")
  else
    echo "ERROR: Could not find Kubernetes CA certificate"
    exit 1
  fi
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
chmod 600 "${TEST_DIR}/vk-kubeconfig.yaml"

# ---------------------------------------------------------------------------
# Generate VK config
# ---------------------------------------------------------------------------
cat > "${TEST_DIR}/vk-config.yaml" <<EOF
InterlinkURL: "http://0.0.0.0"
InterlinkPort: "3000"
VerboseLogging: true
ErrorsOnlyLogging: false
ServiceAccount: "virtual-kubelet"
Namespace: default
VKTokenFile: ""
Resources:
  CPU: "100"
  Memory: "128Gi"
  Pods: "100"
HTTP:
  Insecure: true
KubeletHTTP:
  Insecure: true
EOF

# ---------------------------------------------------------------------------
# Start Virtual Kubelet as a background host process
# ---------------------------------------------------------------------------
echo ""
echo "=== Starting Virtual Kubelet ==="
POD_IP=$(hostname -I | awk '{print $1}')

NODENAME=virtual-kubelet \
  KUBELET_PORT=10251 \
  KUBELET_URL=0.0.0.0 \
  POD_IP="${POD_IP}" \
  CONFIGPATH="${TEST_DIR}/vk-config.yaml" \
  KUBECONFIG="${TEST_DIR}/vk-kubeconfig.yaml" \
  nohup "${TEST_DIR}/vk" > "${TEST_DIR}/vk.log" 2>&1 &

VK_PID=$!
echo "${VK_PID}" > "${TEST_DIR}/vk.pid"
echo "Virtual Kubelet started with PID: ${VK_PID}"

# ---------------------------------------------------------------------------
# Wait for virtual-kubelet node to register with K3s
# ---------------------------------------------------------------------------
export KUBECONFIG=/etc/rancher/k3s/k3s.yaml

echo "Waiting for virtual-kubelet node to register..."
for i in $(seq 1 60); do
  if kubectl get node virtual-kubelet &>/dev/null; then
    echo "✓ virtual-kubelet node registered!"
    break
  fi
  # Check if the VK process is still running
  if ! kill -0 "${VK_PID}" 2>/dev/null; then
    echo "ERROR: Virtual Kubelet process died!"
    echo "VK logs (last 50 lines):"
    tail -50 "${TEST_DIR}/vk.log" || true
    exit 1
  fi
  echo "  Waiting for VK node... ($i/60)"
  sleep 5
done

kubectl get node virtual-kubelet || {
  echo "ERROR: virtual-kubelet node did not register in time"
  echo "VK logs (last 100 lines):"
  tail -100 "${TEST_DIR}/vk.log" || true
  echo "interLink API logs:"
  docker logs interlink-api --tail=50 || true
  exit 1
}

# Wait for virtual-kubelet node to reach Ready condition
echo "Waiting for virtual-kubelet node to become Ready..."
if ! kubectl wait --for=condition=Ready node/virtual-kubelet --timeout=300s; then
  echo "ERROR: virtual-kubelet node did not become Ready in time"
  echo "Node status:"
  kubectl describe node virtual-kubelet || true
  echo "VK logs (last 100 lines):"
  tail -100 "${TEST_DIR}/vk.log" || true
  echo "interLink API logs:"
  docker logs interlink-api --tail=50 || true
  echo "SLURM plugin logs:"
  docker logs interlink-plugin --tail=50 || true
  exit 1
fi
echo "✓ virtual-kubelet node is Ready"

# Approve any pending CSRs (required for kubelet log access)
echo "Approving CSRs..."
kubectl get csr -o name | xargs -r kubectl certificate approve || true

echo ""
echo "=== interLink e2e test environment is ready ==="
echo "  KUBECONFIG:    /etc/rancher/k3s/k3s.yaml"
echo "  Test dir:      ${TEST_DIR}"
echo "  VK PID:        ${VK_PID}"
echo "  VK logs:       ${TEST_DIR}/vk.log"
echo "  API logs:      ${TEST_DIR}/interlink-api.log"
echo "  Plugin logs:   ${TEST_DIR}/interlink-plugin.log"
