#!/bin/bash
# k3s-test-cleanup.sh - Cleanup K3s test environment

set -e

# Get test directory from previous setup
if [ -f /tmp/interlink-test-dir.txt ]; then
    TEST_DIR=$(cat /tmp/interlink-test-dir.txt)
else
    echo "WARNING: Test directory not found. Skipping cleanup."
    exit 0
fi

echo "=== Cleaning up K3s test environment ==="
echo "Test directory: ${TEST_DIR}"

# Stop background processes
echo "Stopping background processes..."
if [ -f "${TEST_DIR}/vk.pid" ]; then
    kill $(cat "${TEST_DIR}/vk.pid") 2>/dev/null || true
    echo "Stopped Virtual Kubelet"
fi

# Stop Docker containers
echo "Stopping Docker containers..."
docker stop interlink-api interlink-plugin 2>/dev/null || true
docker rm interlink-api interlink-plugin 2>/dev/null || true
echo "Stopped and removed containers"

# Stop K3s
if [ -f "${TEST_DIR}/k3s.pid" ]; then
    echo "Stopping K3s..."
    sudo k3s-killall.sh || true
    echo "Stopped K3s"
fi

# Remove test directory
if [ -d "${TEST_DIR}" ]; then
    echo "Removing test directory: ${TEST_DIR}"
    rm -rf "${TEST_DIR}"
fi

# Remove test directory reference
rm -f /tmp/interlink-test-dir.txt

echo "✓ Cleanup complete!"
