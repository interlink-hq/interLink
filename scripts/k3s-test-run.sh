#!/bin/bash
# k3s-test-run.sh - Run integration tests on K3s cluster

set -e

# Get test directory from previous setup
if [ -f /tmp/interlink-test-dir.txt ]; then
  TEST_DIR=$(cat /tmp/interlink-test-dir.txt)
else
  echo "ERROR: Test directory not found. Did you run k3s-test-setup.sh first?"
  exit 1
fi

echo "=== Running interLink integration tests ==="
echo "Test directory: ${TEST_DIR}"

export KUBECONFIG=/etc/rancher/k3s/k3s.yaml

# Check cluster status
echo "Checking cluster status..."
kubectl get nodes
kubectl get pods -A

# Wait for virtual-kubelet node to be present and Ready
echo "Waiting for virtual-kubelet node..."
for i in {1..30}; do
  if kubectl get node virtual-kubelet &>/dev/null; then
    echo "Virtual-kubelet node found!"
    break
  fi
  echo "Waiting for virtual-kubelet node... ($i/30)"
  sleep 2
done

kubectl get node virtual-kubelet || {
  echo "ERROR: virtual-kubelet node not found!"
  echo "All nodes:"
  kubectl get nodes || true
  echo "All pods:"
  kubectl get pods -A || true
  echo "VK process (host):"
  VK_PID_FILE="${TEST_DIR}/vk.pid"
  if [ -f "${VK_PID_FILE}" ]; then
    VK_PID=$(cat "${VK_PID_FILE}")
    echo "VK PID ${VK_PID} running: $(kill -0 "${VK_PID}" 2>/dev/null && echo yes || echo no)"
    echo "VK logs (last 50 lines):"
    tail -50 "${TEST_DIR}/vk.log" || true
  fi
  echo "interLink API logs:"
  docker logs interlink-api --tail=50 || true
  exit 1
}

# Ensure the node is Ready before running tests
echo "Waiting for virtual-kubelet node to be Ready..."
if ! kubectl wait --for=condition=Ready node/virtual-kubelet --timeout=120s; then
  echo "ERROR: virtual-kubelet node is not Ready"
  echo "Node status:"
  kubectl describe node virtual-kubelet || true
  VK_PID_FILE="${TEST_DIR}/vk.pid"
  if [ -f "${VK_PID_FILE}" ]; then
    echo "VK logs (last 100 lines):"
    tail -100 "${TEST_DIR}/vk.log" || true
  fi
  echo "interLink API logs:"
  docker logs interlink-api --tail=50 || true
  echo "SLURM plugin logs:"
  docker logs interlink-plugin --tail=50 || true
  exit 1
fi
echo "✓ virtual-kubelet node is Ready"

# Approve any pending CSRs
echo "Approving CSRs..."
kubectl get csr -o name | xargs -r kubectl certificate approve || true

# Get project root to access submodule
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

# Initialize test submodule if not already done
if [ ! -f "${PROJECT_ROOT}/test/vk-test-set/setup.py" ]; then
  echo "Initializing test/vk-test-set submodule..."
  cd "${PROJECT_ROOT}"
  git submodule update --init test/vk-test-set
fi

# Use test suite from submodule
echo "Using vk-test-set from submodule..."
cd "${PROJECT_ROOT}/test/vk-test-set"

# Create test configuration
echo "Creating test configuration..."
cat >vktest_config.yaml <<EOF
target_nodes:
  - virtual-kubelet

required_namespaces:
  - default
  - kube-system

timeout_multiplier: 10.
values:
  namespace: default

  annotations: {}

  tolerations:
    - key: virtual-node.interlink/no-schedule
      operator: Exists
      effect: NoSchedule
EOF

# Setup Python virtual environment and install vk-test-set (matching ci/main.go)
echo "Setting up Python environment..."
python3 -m venv .venv
source .venv/bin/activate
pip3 install -e ./ || {
  echo "ERROR: Failed to install vk-test-set"
  exit 1
}

echo "vk-test-set installed successfully"

# Run tests
echo "Running integration tests..."
echo "========================================="

# Activate venv and run pytest (matching ci/main.go pattern)
source .venv/bin/activate
export KUBECONFIG=/etc/rancher/k3s/k3s.yaml
pytest -v -k "not rclone and not limits and not stress and not multi-init and not fail" 2>&1 | tee "${TEST_DIR}/test-results.log"
TEST_EXIT_CODE=${PIPESTATUS[0]}

echo "========================================="
echo ""

if [ ${TEST_EXIT_CODE} -eq 0 ]; then
  echo "✓ All VK integration tests passed!"
else
  echo "✗ Some VK integration tests failed (exit code: ${TEST_EXIT_CODE})"
  echo "  - Test results: ${TEST_DIR}/test-results.log"
  echo "  - Plugin logs:  ${TEST_DIR}/interlink-plugin.log"
  echo "  - API logs:     ${TEST_DIR}/interlink-api.log"
  echo "  - VK logs:      ${TEST_DIR}/vk.log"
fi

# ---------------------------------------------------------------------------
# Port-forwarding network tests (rathole tunnel, PR #529)
# Tests the TCP and WebSocket tunnel backends independently of Kubernetes.
# The rathole containers were started by k3s-test-setup.sh; pytest connects
# to the exposed host ports (18080, 18082, 19090) to verify end-to-end flow.
# ---------------------------------------------------------------------------
echo ""
echo "=== Running port-forwarding network tests ==="
echo "========================================="

PF_DIR="${PROJECT_ROOT}/test/portforward"

# Create an isolated venv so portforward deps don't interfere with vk-test-set.
python3 -m venv "${TEST_DIR}/.venv-portforward"
# shellcheck source=/dev/null
source "${TEST_DIR}/.venv-portforward/bin/activate"

# Upgrade pip first, then install deps directly to avoid hatchling build-backend
# issues on older pip versions (Python 3.9 system pip on some distros).
pip install -q --upgrade pip
pip install -q pytest requests pytest-timeout "kubernetes>=28.0" || {
  echo "ERROR: Failed to install portforward test dependencies"
  deactivate
  PF_EXIT_CODE=1
}

if [ "${PF_EXIT_CODE:-0}" -ne 1 ]; then
  cd "${PF_DIR}"

  # Give rathole clients extra time to connect in the CI environment.
  # The annotation-format and isolation tests run without Docker, so they
  # pass even if the tunnel containers haven't finished handshaking yet.
  TUNNEL_WAIT_TIMEOUT=90 \
    pytest -v \
    2>&1 | tee "${TEST_DIR}/portforward-test-results.log"
  PF_EXIT_CODE=${PIPESTATUS[0]}

  deactivate
fi

echo "========================================="
echo ""

if [ "${PF_EXIT_CODE:-0}" -eq 0 ]; then
  echo "✓ All port-forwarding tunnel tests passed!"
else
  echo "✗ Some port-forwarding tunnel tests failed (exit code: ${PF_EXIT_CODE})"
  echo "  - Test results: ${TEST_DIR}/portforward-test-results.log"
  echo "  - Rathole server (TCP):"
  docker compose \
    -f "${PROJECT_ROOT}/test/portforward/docker-compose.yml" \
    --project-name interlink-portforward \
    logs rathole-server-tcp 2>/dev/null | tail -30 || true
  echo "  - Rathole server (WS):"
  docker compose \
    -f "${PROJECT_ROOT}/test/portforward/docker-compose.yml" \
    --project-name interlink-portforward \
    logs rathole-server-ws 2>/dev/null | tail -30 || true
fi

# Combine both exit codes: fail if either test suite failed.
OVERALL_EXIT_CODE=0
[ "${TEST_EXIT_CODE}" -ne 0 ] && OVERALL_EXIT_CODE="${TEST_EXIT_CODE}"
[ "${PF_EXIT_CODE:-0}" -ne 0 ] && OVERALL_EXIT_CODE="${PF_EXIT_CODE}"

if [ "${OVERALL_EXIT_CODE}" -eq 0 ]; then
  echo "✓ All test suites passed!"
else
  echo "✗ One or more test suites failed"
  echo "  VK integration tests: $([ ${TEST_EXIT_CODE} -eq 0 ] && echo PASS || echo FAIL)"
  echo "  Port-forwarding tests: $([ ${PF_EXIT_CODE:-0} -eq 0 ] && echo PASS || echo FAIL)"
fi

exit ${OVERALL_EXIT_CODE}
