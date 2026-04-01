#!/bin/bash
# k3s-test-cleanup.sh - Clean up ephemeral K3s integration test environment
#
# Usage: ./scripts/k3s-test-cleanup.sh
# Requirements: sudo access (for K3s uninstall)

set -e

echo "=== Cleaning up interLink integration test environment ==="

# ---------------------------------------------------------------------------
# Stop Virtual Kubelet host process
# ---------------------------------------------------------------------------
if [ -f /tmp/interlink-test-dir.txt ]; then
  TEST_DIR=$(cat /tmp/interlink-test-dir.txt)
  if [ -f "${TEST_DIR}/vk.pid" ]; then
    VK_PID=$(cat "${TEST_DIR}/vk.pid")
    echo "Stopping Virtual Kubelet (PID: ${VK_PID})..."
    kill "${VK_PID}" 2>/dev/null || true
    # Wait briefly for graceful shutdown
    sleep 2
    kill -9 "${VK_PID}" 2>/dev/null || true
  fi
else
  echo "No test directory file found at /tmp/interlink-test-dir.txt"
fi

# Kill any remaining VK processes by binary name
pkill -f "interlink-test.*vk$" 2>/dev/null || true

# ---------------------------------------------------------------------------
# Stop and remove Docker containers
# ---------------------------------------------------------------------------
echo "Removing Docker containers..."
docker stop interlink-api 2>/dev/null || true
docker rm interlink-api 2>/dev/null || true
docker stop interlink-plugin 2>/dev/null || true
docker rm interlink-plugin 2>/dev/null || true

# ---------------------------------------------------------------------------
# Remove Docker network
# ---------------------------------------------------------------------------
echo "Removing Docker network..."
docker network rm interlink-net 2>/dev/null || true

# ---------------------------------------------------------------------------
# Stop and uninstall K3s
# ---------------------------------------------------------------------------
echo "Stopping K3s..."
if [ -f /usr/local/bin/k3s-uninstall.sh ]; then
  sudo /usr/local/bin/k3s-uninstall.sh 2>/dev/null || true
else
  echo "K3s uninstall script not found, skipping."
fi

# ---------------------------------------------------------------------------
# Remove test directory
# ---------------------------------------------------------------------------
if [ -f /tmp/interlink-test-dir.txt ]; then
  TEST_DIR=$(cat /tmp/interlink-test-dir.txt)
  echo "Removing test directory: ${TEST_DIR}"
  rm -rf "${TEST_DIR}" 2>/dev/null || true
  rm -f /tmp/interlink-test-dir.txt
fi

echo ""
echo "✓ Cleanup complete"
