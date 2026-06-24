"""
Pytest fixtures for the rathole port-forwarding integration tests.

The tests assume that docker compose is running (docker compose up -d) before
pytest is invoked.  Each fixture waits up to WAIT_TIMEOUT seconds for its
target URL to become reachable so that slow container start-ups don't cause
false failures.

For Kubernetes TLS resource tests (test_tls_k8s.py) set KUBECONFIG before
running; those tests are skipped automatically when KUBECONFIG is absent.
"""

import time
import os
import socket

import pytest
import requests

# ── tuneable constants ──────────────────────────────────────────────────────
WAIT_TIMEOUT = int(os.environ.get("TUNNEL_WAIT_TIMEOUT", "60"))
WAIT_INTERVAL = 1.0

TCP_HTTP_URL = os.environ.get("TCP_HTTP_URL", "http://localhost:18080")
TCP_METRICS_URL = os.environ.get("TCP_METRICS_URL", "http://localhost:19090")
WS_HTTP_URL = os.environ.get("WS_HTTP_URL", "http://localhost:18082")


# ── helpers ─────────────────────────────────────────────────────────────────

def _wait_for_http(url: str, timeout: int = WAIT_TIMEOUT) -> None:
    """
    Poll *url* until it returns a non-5xx HTTP response or *timeout* seconds
    elapse, whichever comes first.

    Raises TimeoutError if the service does not become reachable in time.
    """
    deadline = time.monotonic() + timeout
    last_exc: Exception = RuntimeError("never tried")

    while time.monotonic() < deadline:
        try:
            resp = requests.get(url, timeout=3)
            if resp.status_code < 500:
                return
        except (requests.exceptions.ConnectionError,
                requests.exceptions.Timeout) as exc:
            last_exc = exc
        time.sleep(WAIT_INTERVAL)

    raise TimeoutError(
        f"Service at {url!r} did not become reachable within {timeout}s "
        f"(last error: {last_exc})"
    )


def _wait_for_tcp(host: str, port: int, timeout: int = WAIT_TIMEOUT) -> None:
    """Wait until a raw TCP connection to host:port succeeds."""
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            with socket.create_connection((host, port), timeout=2):
                return
        except OSError:
            time.sleep(WAIT_INTERVAL)
    raise TimeoutError(
        f"TCP port {host}:{port} did not open within {timeout}s"
    )


# ── session-scoped fixtures ──────────────────────────────────────────────────

@pytest.fixture(scope="session")
def tcp_http_url() -> str:
    """
    URL reachable through the TCP-mode rathole tunnel.
    Points at the nginx backend running in the isolated "remote" network.
    """
    _wait_for_http(TCP_HTTP_URL)
    return TCP_HTTP_URL


@pytest.fixture(scope="session")
def tcp_metrics_url() -> str:
    """
    URL reachable on the secondary port (9090) through the TCP-mode tunnel.
    Tests that multi-port forwarding works correctly.
    """
    _wait_for_http(TCP_METRICS_URL)
    return TCP_METRICS_URL


@pytest.fixture(scope="session")
def ws_http_url() -> str:
    """
    URL reachable through the WebSocket-mode rathole tunnel.
    This exercises the DefaultRatholeWSCommand code path in interLink.
    """
    _wait_for_http(WS_HTTP_URL)
    return WS_HTTP_URL


@pytest.fixture(scope="session")
def http_session() -> requests.Session:
    """A shared requests Session with a sensible default timeout."""
    session = requests.Session()
    session.headers.update({"User-Agent": "interlink-portforward-test/1.0"})
    return session


# ── Kubernetes fixtures (used by test_tls_k8s.py) ────────────────────────────

@pytest.fixture(scope="session")
def k8s_core_v1():
    """
    kubernetes.client.CoreV1Api instance.
    Skips tests in the current session if KUBECONFIG is not set.
    """
    kubeconfig = os.environ.get("KUBECONFIG")
    if not kubeconfig:
        pytest.skip("KUBECONFIG not set")
    try:
        from kubernetes import client as k8s_client
        from kubernetes import config as k8s_config
        k8s_config.load_kube_config(kubeconfig)
        return k8s_client.CoreV1Api()
    except Exception as exc:  # noqa: BLE001
        pytest.skip(f"Cannot initialise Kubernetes client: {exc}")


@pytest.fixture(scope="session")
def k8s_custom_api():
    """
    kubernetes.client.CustomObjectsApi instance.
    Skips tests in the current session if KUBECONFIG is not set.
    """
    kubeconfig = os.environ.get("KUBECONFIG")
    if not kubeconfig:
        pytest.skip("KUBECONFIG not set")
    try:
        from kubernetes import client as k8s_client
        from kubernetes import config as k8s_config
        k8s_config.load_kube_config(kubeconfig)
        return k8s_client.CustomObjectsApi()
    except Exception as exc:  # noqa: BLE001
        pytest.skip(f"Cannot initialise Kubernetes client: {exc}")


@pytest.fixture(scope="session")
def vk_node_name(k8s_core_v1):
    """
    Returns the name of the virtual-kubelet node, or skips if not found.
    """
    try:
        nodes = k8s_core_v1.list_node(
            field_selector="metadata.name=virtual-kubelet"
        ).items
    except Exception as exc:  # noqa: BLE001
        pytest.skip(f"Cannot list Kubernetes nodes: {exc}")
    if not nodes:
        pytest.skip("virtual-kubelet node not found in cluster")
    return nodes[0].metadata.name

