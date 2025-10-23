#!/bin/bash
# k3s-test-setup.sh - Setup ephemeral K3s cluster for interLink integration tests
# Follows the pattern from example/debug.sh

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
TEST_DIR="/tmp/interlink-test-$$"

setup_k3s_cluster() {
  echo "=== Setting up ephemeral K3s cluster for interLink integration tests ==="
  echo "Test directory: ${TEST_DIR}"

  # Create test directory
  mkdir -p "${TEST_DIR}"
  export TEST_DIR

  # Save test directory for other scripts
  echo "${TEST_DIR}" >/tmp/interlink-test-dir.txt

  # Download K3s if not already installed
  if ! command -v k3s &>/dev/null; then
    echo "Downloading K3s..."
    curl -sfL https://get.k3s.io | INSTALL_K3S_VERSION=v1.31.4+k3s1 sh -
  else
    echo "K3s already installed: $(k3s --version)"
  fi

  # Start K3s
  echo "Starting K3s cluster..."
  sudo k3s server --disable traefik --write-kubeconfig-mode=644 >"${TEST_DIR}/k3s.log" 2>&1 &
  echo $! >"${TEST_DIR}/k3s.pid"

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
}

# function to build Docker images
build_docker_images() {
  echo "Building interLink API Docker image..."
  docker build -f "${PROJECT_ROOT}/docker/Dockerfile.interlink" \
    -t interlink:ci-test "${PROJECT_ROOT}"

  echo "Using local SLURM plugin from submodule..."
  # Initialize submodule if not already done
  if [ ! -f "${PROJECT_ROOT}/plugins/slurm/docker/Dockerfile" ]; then
    echo "Initializing plugins/slurm submodule..."
    cd "${PROJECT_ROOT}"
    git submodule update --init plugins/slurm
    cd -
  fi

  echo "Building SLURM plugin Docker image from submodule..."
  docker build -f "${PROJECT_ROOT}/plugins/slurm/docker/Dockerfile" \
    -t interlink-slurm-plugin:ci-test "${PROJECT_ROOT}/plugins/slurm"

  echo "Docker images built successfully"
  docker images | grep -E "interlink|slurm"

  # Store images as tarball in /var/lib/rancher/k3s/agent/images/
  # This allows K3s to load them on restart
  # Restart k3 afterwords to pick up new images
  echo "Loading images into K3s..."
  IMAGE_TARBALL_DIR="/var/lib/rancher/k3s/agent/images"
  sudo mkdir -p "${IMAGE_TARBALL_DIR}"
  echo "Saving images to ${IMAGE_TARBALL_DIR}..."

  ## No need as long as we deploy components trhough docker run
  # sudo docker save interlink:ci-test -o "${IMAGE_TARBALL_DIR}/interlink_ci_test.tar"
  # sudo docker save interlink-slurm-plugin:ci-test -o "${IMAGE_TARBALL_DIR}/interlink_slurm_plugin_ci_test.tar"
}

# Functions to install components

install_components() {
  # Create test directory
  mkdir -p "${TEST_DIR}"
  export TEST_DIR

  # Create plugin configuration
  echo "Creating plugin configuration..."
  cat >"${TEST_DIR}/plugin-config.yaml" <<EOF
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
  cat >"${TEST_DIR}/interlink-config.yaml" <<EOF
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

  # Install Virtual Kubelet via Helm (following ci/main.go pattern)
  echo "Installing Virtual Kubelet via Helm..."

  # Build VK Docker image from source
  echo "Building Virtual Kubelet Docker image from source..."
  cd "${PROJECT_ROOT}"

  # Build the VK binary
  CGO_ENABLED=0 GOOS=linux go build -o bin/vk cmd/virtual-kubelet/main.go

  # Create a minimal Dockerfile for VK
  cat >"${TEST_DIR}/Dockerfile.vk" <<'DOCKERFILE_EOF'
FROM alpine:latest
COPY bin/vk /bin/vk
ENTRYPOINT ["/bin/vk"]
DOCKERFILE_EOF

  # Build VK image
  docker build -f "${TEST_DIR}/Dockerfile.vk" -t virtual-kubelet:ci-test "${PROJECT_ROOT}"

  # Load VK image into K3s
  echo "Loading images into K3s..."
  sudo docker save virtual-kubelet:ci-test | sudo k3s ctr images import -

  # Create Helm values file (following ci/main.go pattern)
  echo "Creating Helm values file..."
  cat >"${TEST_DIR}/vk-helm-values.yaml" <<EOF
nodeName: virtual-kubelet

interlink:
  enabled: false
  address: http://0.0.0.0
  port: "3000"
  disableProjectedVolumes: true

virtualNode:
  image: "virtual-kubelet:ci-test"
  resources:
    CPUs: "100"
    memGiB: "128"
    pods: "100"
  HTTPProxies:
    HTTP: null
    HTTPs: null
  HTTP:
    insecure: true

OAUTH:
  enabled: false
EOF

  # Install Helm if not present
  if ! command -v helm &>/dev/null; then
    echo "Installing Helm..."
    curl -fsSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash
  fi

  # Initialize helm submodule if not already done
  if [ ! -d "${PROJECT_ROOT}/helm/interlink" ]; then
    echo "Initializing helm submodule..."
    cd "${PROJECT_ROOT}"
    git submodule update --init helm
    cd -
  fi

  # Install Virtual Kubelet using local Helm chart from submodule
  echo "Installing Virtual Kubelet via local Helm chart from submodule..."
  export KUBECONFIG=/etc/rancher/k3s/k3s.yaml
  helm install \
    --create-namespace \
    -n interlink \
    virtual-node \
    "${PROJECT_ROOT}/helm/interlink" \
    --values "${TEST_DIR}/vk-helm-values.yaml"

  # Wait for VK deployment to be ready
  echo "Waiting for Virtual Kubelet deployment to be ready..."
  kubectl wait --for=condition=Available deployment/virtual-kubelet-node -n interlink --timeout=300s || kubectl describe deployment virtual-kubelet-node -n interlink

  # Check pod status
  echo "Virtual Kubelet pod status:"
  kubectl get pods -n interlink -l app=virtual-kubelet

  # Wait for VK to register with K8s
  sleep 15

  echo "Checking for virtual-kubelet node..."
  kubectl get nodes

  if ! kubectl get node virtual-kubelet 2>/dev/null; then
    echo "WARNING: Virtual Kubelet node not found yet"
    echo "Virtual Kubelet logs:"
    kubectl logs -n interlink -l app=virtual-kubelet --tail=50 || true
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
  echo "  - Virtual Kubelet: kubectl logs -n interlink -l app=virtual-kubelet"
  echo ""
  echo "Running components:"
  echo "  - SLURM plugin container: interlink-plugin (port 4000)"
  echo "  - interLink API container: interlink-api (port 3000)"
  echo "  - Virtual Kubelet: Helm deployment in interlink namespace"
  echo ""
}

## Bootstrap k3s only if not already running OR if KUBECONFIG is present.
## if k3s is already running, go directly to building images and starting containers.
if [ -f "$KUBECONFIG" ]; then
  echo "KUBECONFIG is set and file exists. Assuming K3s is already running. Skipping cluster setup."
  if ! command -v k3s &>/dev/null; then
    echo "Downloading K3s..."
    curl -sfL https://get.k3s.io | INSTALL_K3S_SKIP_ENABLE=true INSTALL_K3S_VERSION=v1.31.4+k3s1 sh -
  else
    echo "K3s already installed: $(k3s --version)"
  fi
  build_docker_images
  install_components
  exit 0
else
  setup_k3s_cluster
  build_docker_images
  install_components
fi
