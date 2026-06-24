# Port-Forwarding Tunnel Tests

Integration tests for the rathole tunnel backend added in [PR #529](https://github.com/interlink-hq/interLink/pull/529), which abstracts the port-forwarding middleware to support `rathole` as an alternative to `wstunnel`.

## What is tested

| Test class | Transport | interLink code path |
|---|---|---|
| `TestTCPTunnel` | TCP (default) | `TunnelType="rathole"`, `RatholeCAIssuerName` set → TLS mode |
| `TestWebSocketTunnel` | WebSocket | `TunnelType="rathole"`, no CA issuer → `DefaultRatholeWSCommand` |
| `TestTCPMultiPort` | TCP | Multiple `[server.services.pNNNN]` entries in `rathole-template.yaml` |
| `TestNetworkIsolation` | — | Backend is unreachable except through the tunnel |
| `TestAnnotationCommandFormat` | — | Validates `DefaultRatholeWSCommand` / `DefaultRatholeCommand` format verbs |

## Network topology

```
[remote network – isolated "HPC" side]
  backend (nginx:alpine)
    port 80  → "Hello from remote backend (port 80)"
    port 9090 → "Metrics from remote backend (port 9090)"

[cluster network – "Kubernetes" side]
  rathole-server-tcp  (TCP transport)   → host:18080, host:19090
  rathole-server-ws   (WS transport)    → host:18082

[bridge: remote + cluster]
  rathole-client-tcp  (forwards backend:80 → server:8080, backend:9090 → server:9090)
  rathole-client-ws   (forwards backend:80 → server:8082  via WebSocket)
```

The backend has **no** ports exposed to the host, so the only path to reach it is through a rathole tunnel — matching the real interLink deployment where the remote service is inside the HPC network.

## Prerequisites

- Docker Engine ≥ 20.10 with the Compose plugin
- Python ≥ 3.8 + pip

## Quickstart

```bash
# 1. Start the test environment
docker compose up -d

# 2. Install Python dependencies
pip install -e .
# or: pip install pytest requests pytest-timeout

# 3. Run all tests
pytest -v

# 4. Tear down
docker compose down -v
```

### Run a specific test class

```bash
# TCP tunnel only
pytest -v test_tunnel.py::TestTCPTunnel

# WebSocket tunnel only
pytest -v test_tunnel.py::TestWebSocketTunnel

# Multi-port forwarding
pytest -v test_tunnel.py::TestTCPMultiPort

# Annotation command format checks (no docker needed)
pytest -v test_tunnel.py::TestAnnotationCommandFormat
```

### Useful docker compose commands

```bash
# Check container status
docker compose ps

# Stream container logs (useful for debugging tunnel handshake)
docker compose logs -f

# Restart a single service (e.g. to test tunnel reconnect)
docker compose restart rathole-client-tcp

# Watch rathole server logs
docker compose logs -f rathole-server-tcp
```

## Environment variables

| Variable | Default | Description |
|---|---|---|
| `TCP_HTTP_URL` | `http://localhost:18080` | TCP tunnel HTTP endpoint |
| `TCP_METRICS_URL` | `http://localhost:19090` | TCP tunnel secondary port |
| `WS_HTTP_URL` | `http://localhost:18082` | WebSocket tunnel HTTP endpoint |
| `TUNNEL_WAIT_TIMEOUT` | `60` | Seconds to wait for tunnels to become ready |

## Troubleshooting

**Tests time out waiting for tunnels**  
Run `docker compose logs rathole-client-tcp` to see if the client is failing to connect. The client uses `restart: on-failure` so it will retry automatically once the server is up. Increase `TUNNEL_WAIT_TIMEOUT` if your machine is slow.

**`rapiz1/rathole:v0.5.0` image not found**  
Ensure Docker Hub access is available. The image is the same one used in `rathole-template.yaml`.

**Port conflicts on 18080 / 18082 / 19090**  
Override with environment variables before running `docker compose up -d`, or edit the `ports:` section in `docker-compose.yml`.
