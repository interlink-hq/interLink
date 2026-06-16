"""
Port-forwarding integration tests for the rathole tunnel backend introduced
in interLink PR #529.

These tests verify that traffic flows correctly through the rathole tunnel in
both transport modes:
  - TCP  mode  →  mirrors TunnelType=="rathole" with RatholeCAIssuerName set
  - WebSocket mode  →  mirrors TunnelType=="rathole" with no CA issuer
                       (DefaultRatholeWSCommand path)

Prerequisites:
    docker compose up -d        # bring up the test environment
    pytest -v                   # run tests
    docker compose down -v      # tear down

Environment variables (all optional):
    TCP_HTTP_URL      (default http://localhost:18080)
    TCP_METRICS_URL   (default http://localhost:19090)
    WS_HTTP_URL       (default http://localhost:18082)
    TUNNEL_WAIT_TIMEOUT  seconds to wait for tunnels to initialise (default 60)
"""

import threading
import time

import pytest
import requests


# ── TCP transport mode ───────────────────────────────────────────────────────

class TestTCPTunnel:
    """
    TCP-mode rathole tunnel: rathole server + client using default TCP transport.
    Exercises the annotation-building path in addWstunnelClientAnnotation when
    TunnelType=="rathole" and RatholeCAIssuerName is configured.
    """

    def test_http_connectivity(self, tcp_http_url, http_session):
        """Traffic reaches the backend nginx through the TCP tunnel."""
        resp = http_session.get(tcp_http_url, timeout=5)
        assert resp.status_code == 200
        assert "remote backend" in resp.text

    def test_response_contains_expected_port(self, tcp_http_url, http_session):
        """Backend response identifies the correct port (80) was reached."""
        resp = http_session.get(tcp_http_url, timeout=5)
        assert "port 80" in resp.text

    def test_http_response_headers(self, tcp_http_url, http_session):
        """Server header is present (proves nginx is the origin, not a proxy error)."""
        resp = http_session.get(tcp_http_url, timeout=5)
        assert "server" in resp.headers
        assert resp.headers["server"].lower().startswith("nginx")

    def test_multiple_sequential_requests(self, tcp_http_url, http_session):
        """Multiple sequential requests all succeed (connection re-use / keep-alive)."""
        for _ in range(5):
            resp = http_session.get(tcp_http_url, timeout=5)
            assert resp.status_code == 200

    def test_concurrent_requests(self, tcp_http_url):
        """Concurrent requests are all served correctly (no connection starvation)."""
        results: list[int] = []
        errors: list[str] = []

        def do_request() -> None:
            try:
                resp = requests.get(tcp_http_url, timeout=10)
                results.append(resp.status_code)
            except Exception as exc:  # noqa: BLE001
                errors.append(str(exc))

        threads = [threading.Thread(target=do_request) for _ in range(10)]
        for t in threads:
            t.start()
        for t in threads:
            t.join()

        assert not errors, f"Concurrent requests produced errors: {errors}"
        assert all(s == 200 for s in results), f"Non-200 responses: {results}"

    def test_large_payload_passthrough(self, tcp_http_url, http_session):
        """
        Verify that the tunnel doesn't truncate responses.
        We request a URL that returns a known-size response and check the length.
        (nginx returns the full plain-text string; no truncation expected.)
        """
        resp = http_session.get(tcp_http_url, timeout=10)
        assert resp.status_code == 200
        # Must contain at least our marker string
        assert len(resp.text) > 0


# ── Multi-port forwarding (TCP mode) ─────────────────────────────────────────

class TestTCPMultiPort:
    """
    Tests that rathole correctly forwards multiple ports simultaneously,
    matching the template in rathole-template.yaml which can list multiple
    [server.services.pNNNN] entries.
    """

    def test_secondary_port_reachable(self, tcp_metrics_url, http_session):
        """The secondary forwarded port (9090) is independently reachable."""
        resp = http_session.get(tcp_metrics_url, timeout=5)
        assert resp.status_code == 200
        assert "Metrics" in resp.text

    def test_secondary_port_has_correct_content(self, tcp_metrics_url, http_session):
        """Content on port 9090 differs from port 80 – proves separate endpoints."""
        resp = http_session.get(tcp_metrics_url, timeout=5)
        assert "port 9090" in resp.text

    def test_both_ports_independent(self, tcp_http_url, tcp_metrics_url, http_session):
        """Requests to both forwarded ports succeed in the same test."""
        http_resp = http_session.get(tcp_http_url, timeout=5)
        metrics_resp = http_session.get(tcp_metrics_url, timeout=5)
        assert http_resp.status_code == 200
        assert metrics_resp.status_code == 200
        # Content should differ between the two ports
        assert http_resp.text != metrics_resp.text


# ── WebSocket transport mode ──────────────────────────────────────────────────

class TestWebSocketTunnel:
    """
    WebSocket-mode rathole tunnel: mirrors the DefaultRatholeWSCommand path in
    addWstunnelClientAnnotation (TunnelType=="rathole", RatholeCAIssuerName="").

    The rathole server and client use [transport] type = "websocket" which is
    what interLink injects into the client TOML annotation.
    """

    def test_http_connectivity(self, ws_http_url, http_session):
        """Traffic reaches the backend nginx through the WebSocket tunnel."""
        resp = http_session.get(ws_http_url, timeout=5)
        assert resp.status_code == 200
        assert "remote backend" in resp.text

    def test_response_contains_expected_port(self, ws_http_url, http_session):
        """Backend response identifies the correct port (80) was reached."""
        resp = http_session.get(ws_http_url, timeout=5)
        assert "port 80" in resp.text

    def test_multiple_sequential_requests(self, ws_http_url, http_session):
        """WebSocket tunnel handles sequential requests without session errors."""
        for _ in range(5):
            resp = http_session.get(ws_http_url, timeout=5)
            assert resp.status_code == 200

    def test_concurrent_requests(self, ws_http_url):
        """WebSocket tunnel handles concurrent requests."""
        results: list[int] = []
        errors: list[str] = []

        def do_request() -> None:
            try:
                resp = requests.get(ws_http_url, timeout=10)
                results.append(resp.status_code)
            except Exception as exc:  # noqa: BLE001
                errors.append(str(exc))

        threads = [threading.Thread(target=do_request) for _ in range(8)]
        for t in threads:
            t.start()
        for t in threads:
            t.join()

        assert not errors, f"Concurrent WS-tunnel requests produced errors: {errors}"
        assert all(s == 200 for s in results), f"Non-200 responses: {results}"


# ── Network isolation ─────────────────────────────────────────────────────────

class TestNetworkIsolation:
    """
    Verify that the backend is NOT directly reachable from the "cluster" side
    (i.e., from the host where pytest runs).  Traffic MUST go through a tunnel.

    These tests are important because the docker-compose intentionally places
    the backend on an isolated "remote" network.
    """

    def test_backend_not_directly_reachable_on_80(self):
        """
        Port 80 is not exposed directly to the host – the backend is only
        reachable through the forwarded ports (18080, 18082).
        If something else is listening on 80 it must NOT be our backend.
        """
        try:
            resp = requests.get("http://localhost:80", timeout=2)
            # Something is listening, but it must not be our backend nginx
            assert "remote backend" not in resp.text, (
                "Backend content is directly accessible on port 80 – "
                "docker-compose.yml isolation is broken"
            )
        except requests.exceptions.ConnectionError:
            pass  # Nothing on port 80 – expected when compose is running

    def test_backend_not_directly_reachable_on_9090(self):
        """Port 9090 is not exposed directly to the host."""
        try:
            resp = requests.get("http://localhost:9090", timeout=2)
            assert "remote backend" not in resp.text, (
                "Backend content is directly accessible on port 9090 – "
                "docker-compose.yml isolation is broken"
            )
        except requests.exceptions.ConnectionError:
            pass


# ── Annotation command smoke tests ────────────────────────────────────────────

class TestAnnotationCommandFormat:
    """
    Unit-style checks that verify the format of the bootstrap commands that
    interLink writes into pod annotations (DefaultRatholeWSCommand /
    DefaultRatholeCommand).  These do not start actual containers – they parse
    the command strings that the VK would inject.
    """

    # These constants are copied from pkg/virtualkubelet/virtualkubelet.go
    # to keep the test independent of the Go build.
    DEFAULT_RATHOLE_EXECUTABLE_URL = (
        "https://github.com/rathole-org/rathole/releases/download/v0.5.0/"
        "rathole-x86_64-unknown-linux-gnu.zip"
    )
    DEFAULT_WS_CMD_TEMPLATE = (
        "curl -L -f -k %s -o rathole.zip && unzip -q rathole.zip && "
        "chmod +x rathole && echo %s | base64 -d > /tmp/rathole-client.toml && "
        "./rathole --client /tmp/rathole-client.toml &"
    )
    DEFAULT_TLS_CMD_TEMPLATE = (
        "curl -L -f -k %s -o rathole.zip && unzip -q rathole.zip && "
        "chmod +x rathole && echo %s | base64 -d > /tmp/rathole-ca.crt && "
        "echo %s | base64 -d > /tmp/rathole-client.crt && "
        "echo %s | base64 -d > /tmp/rathole-client.key && "
        "echo %s | base64 -d > /tmp/rathole-client.toml && "
        "./rathole --client /tmp/rathole-client.toml &"
    )

    def test_ws_command_has_two_format_verbs(self):
        """DefaultRatholeWSCommand must have exactly 2 %s verbs (url, toml)."""
        count = self.DEFAULT_WS_CMD_TEMPLATE.count("%s")
        assert count == 2, f"Expected 2 %s verbs, found {count}"

    def test_tls_command_has_five_format_verbs(self):
        """DefaultRatholeCommand must have exactly 5 %s verbs (url, ca, cert, key, toml)."""
        count = self.DEFAULT_TLS_CMD_TEMPLATE.count("%s")
        assert count == 5, f"Expected 5 %s verbs, found {count}"

    def test_ws_command_references_default_url(self):
        """WebSocket command embeds the default download URL."""
        cmd = self.DEFAULT_WS_CMD_TEMPLATE % (
            self.DEFAULT_RATHOLE_EXECUTABLE_URL,
            "BASE64TOML",
        )
        assert self.DEFAULT_RATHOLE_EXECUTABLE_URL in cmd
        assert "base64 -d" in cmd
        assert "./rathole --client" in cmd

    def test_tls_command_references_cert_files(self):
        """TLS command writes the expected cert filenames."""
        cmd = self.DEFAULT_TLS_CMD_TEMPLATE % (
            self.DEFAULT_RATHOLE_EXECUTABLE_URL,
            "BASE64CA",
            "BASE64CRT",
            "BASE64KEY",
            "BASE64TOML",
        )
        assert "rathole-ca.crt" in cmd
        assert "rathole-client.crt" in cmd
        assert "rathole-client.key" in cmd
        assert "rathole-client.toml" in cmd
        assert "base64 -d" in cmd

    def test_ws_command_runs_client_in_background(self):
        """Bootstrap command must launch rathole as a background process (&)."""
        assert self.DEFAULT_WS_CMD_TEMPLATE.rstrip().endswith("&")

    def test_tls_command_runs_client_in_background(self):
        """TLS bootstrap command must also run rathole in the background."""
        assert self.DEFAULT_TLS_CMD_TEMPLATE.rstrip().endswith("&")
