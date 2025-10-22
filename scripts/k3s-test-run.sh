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

# Wait for virtual-kubelet node to be ready
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
    echo "Virtual Kubelet deployment status:"
    kubectl get deployment -n interlink virtual-kubelet-node || true
    echo "Virtual Kubelet pod status:"
    kubectl get pods -n interlink -l app=virtual-kubelet || true
    echo "Virtual Kubelet logs:"
    kubectl logs -n interlink -l app=virtual-kubelet --tail=100 || true
    exit 1
}

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
cat > vktest_config.yaml <<EOF
target_nodes:
  - virtual-kubelet

required_namespaces:
  - default
  - kube-system

timeout_multiplier: 10.
values:
  namespace: default

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
pytest -v -k "not rclone and not limits" 2>&1 | tee "${TEST_DIR}/test-results.log"
TEST_EXIT_CODE=${PIPESTATUS[0]}

echo "========================================="
echo ""

if [ ${TEST_EXIT_CODE} -eq 0 ]; then
    echo "✓ All tests passed!"
else
    echo "✗ Some tests failed (exit code: ${TEST_EXIT_CODE})"
    echo ""
    echo "Check logs for details:"
    echo "  - Test results: ${TEST_DIR}/test-results.log"
    echo "  - Plugin: ${TEST_DIR}/plugin.log"
    echo "  - interLink: ${TEST_DIR}/interlink.log"
    echo "  - Virtual Kubelet: kubectl logs -n interlink -l app=virtual-kubelet"
fi

exit ${TEST_EXIT_CODE}
