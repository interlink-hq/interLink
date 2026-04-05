#!/bin/bash
# k3s-test-cleanup.sh - Clean up ephemeral K3s integration test environment
#
# Usage: ./scripts/k3s-test-cleanup.sh
# Requirements: sudo access (for K3s uninstall)

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
# Stop background log-streaming processes
# ---------------------------------------------------------------------------
if [ -f /tmp/interlink-test-dir.txt ]; then
  TEST_DIR=$(cat /tmp/interlink-test-dir.txt)
  for pidfile in "${TEST_DIR}/api-log.pid" "${TEST_DIR}/plugin-log.pid"; do
    if [ -f "${pidfile}" ]; then
      kill "$(cat "${pidfile}")" 2>/dev/null || true
    fi
  done
fi

# ---------------------------------------------------------------------------
# Persist Docker container logs before stopping
# ---------------------------------------------------------------------------
if [ -f /tmp/interlink-test-dir.txt ]; then
  TEST_DIR=$(cat /tmp/interlink-test-dir.txt)
  echo "Saving container logs to ${TEST_DIR}..."
  docker logs interlink-api  > "${TEST_DIR}/interlink-api.log"  2>&1 || true
  docker logs interlink-plugin > "${TEST_DIR}/interlink-plugin.log" 2>&1 || true
  echo "Copying plugin job directories from container..."
  docker cp interlink-plugin:/tmp/.interlink/. "${TEST_DIR}/plugin-jobs/" 2>/dev/null || true
  echo "Copying Slurm logs from container..."
  docker cp interlink-plugin:/var/log/slurm/. "${TEST_DIR}/slurm-logs/" 2>/dev/null || true
fi

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
# Optionally remove test directory
# ---------------------------------------------------------------------------
if [ -f /tmp/interlink-test-dir.txt ]; then
  TEST_DIR=$(cat /tmp/interlink-test-dir.txt)
  if [ "${REMOVE_TEST_DIR}" = "1" ]; then
    echo "Removing test directory: ${TEST_DIR}"
    rm -rf "${TEST_DIR}" 2>/dev/null || true
    rm -f /tmp/interlink-test-dir.txt
  else
    echo "Preserving test directory for debugging: ${TEST_DIR}"
    echo "Set REMOVE_TEST_DIR=1 to remove it and delete /tmp/interlink-test-dir.txt."
  fi
fi

echo ""
echo "✓ Cleanup complete"
