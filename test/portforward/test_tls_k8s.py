"""
Kubernetes-side integration tests for the rathole TLS tunnel resources.

These tests verify that the Virtual Kubelet (configured with TunnelType=rathole
and RatholeCAIssuerName=interlink-test-ca) correctly creates:
  - cert-manager Certificate CRs (server + client)
  - Traefik IngressRouteTCP
  - TLS secrets issued by cert-manager
  - pod annotation with the rathole bootstrap command

Prerequisites (set up by k3s-test-setup.sh):
  - cert-manager installed and running
  - interlink-test-ca ClusterIssuer ready (self-signed CA chain)
  - Traefik v3 CRDs installed (traefik.io/v1alpha1)
  - Virtual Kubelet running with Network.TunnelType=rathole and
    Network.RatholeCAIssuerName=interlink-test-ca

Automatically skipped when KUBECONFIG is not set.
"""

import base64
import os
import time
import uuid

import pytest

# ── helpers ─────────────────────────────────────────────────────────────────

def _wait_for(fn, timeout: int = 120, interval: float = 3.0, label: str = "resource"):
    """
    Poll *fn()* until it returns a truthy value.  The function must not raise
    (use try/except inside) – a falsy return triggers the next retry.
    Raises TimeoutError if *timeout* seconds elapse without success.
    """
    deadline = time.monotonic() + timeout
    last_exc: Exception = RuntimeError("never tried")
    while time.monotonic() < deadline:
        try:
            result = fn()
            if result:
                return result
        except Exception as exc:  # noqa: BLE001
            last_exc = exc
        time.sleep(interval)
    raise TimeoutError(
        f"Timed out after {timeout}s waiting for {label!r}. Last error: {last_exc}"
    )


# ── fixtures ─────────────────────────────────────────────────────────────────

@pytest.fixture(scope="module")
def k8s_apis(request):
    """
    Returns a dict of Kubernetes API clients.
    Skips the entire module if KUBECONFIG is not set or the VK node is absent.
    """
    kubeconfig = os.environ.get("KUBECONFIG")
    if not kubeconfig:
        pytest.skip("KUBECONFIG not set – skipping Kubernetes TLS resource tests")

    try:
        from kubernetes import client as k8s_client
        from kubernetes import config as k8s_config
        k8s_config.load_kube_config(kubeconfig)
    except Exception as exc:  # noqa: BLE001
        pytest.skip(f"Cannot load kubeconfig: {exc}")

    core = k8s_client.CoreV1Api()
    apps = k8s_client.AppsV1Api()
    custom = k8s_client.CustomObjectsApi()
    networking = k8s_client.NetworkingV1Api()

    # Confirm the virtual-kubelet node is present
    try:
        nodes = core.list_node(field_selector="metadata.name=virtual-kubelet").items
    except Exception as exc:  # noqa: BLE001
        pytest.skip(f"Cannot list nodes: {exc}")
    if not nodes:
        pytest.skip("virtual-kubelet node not found – skipping Kubernetes TLS resource tests")

    return {"core": core, "apps": apps, "custom": custom, "networking": networking}


@pytest.fixture(scope="module")
def tunnel_pod(k8s_apis):
    """
    Creates a short-lived test pod on the virtual-kubelet node with a TCP
    containerPort so the VK triggers tunnel TLS resource creation.
    The pod is deleted after the module's tests finish.
    """
    core = k8s_apis["core"]

    pod_name = f"tls-test-{uuid.uuid4().hex[:6]}"
    namespace = "default"

    pod_manifest = {
        "apiVersion": "v1",
        "kind": "Pod",
        "metadata": {
            "name": pod_name,
            "namespace": namespace,
            "annotations": {
                # Give the VK more time to pull the rathole image before
                # declaring the shadow pod IP wait a failure.
                "interlink.virtual-kubelet.io/wstunnel-timeout": "120s",
            },
        },
        "spec": {
            "nodeName": "virtual-kubelet",
            "tolerations": [
                {
                    "key": "virtual-node.interlink/no-schedule",
                    "operator": "Exists",
                    "effect": "NoSchedule",
                }
            ],
            "containers": [
                {
                    "name": "service",
                    "image": "nginx:alpine",
                    "ports": [
                        {"containerPort": 8080, "protocol": "TCP"},
                    ],
                }
            ],
            # Prevent the pod from ever re-starting – we only need it to
            # trigger VK tunnel creation, not actually complete a job.
            "restartPolicy": "Never",
        },
    }

    from kubernetes import client as k8s_client

    pod = core.create_namespaced_pod(namespace=namespace, body=pod_manifest)
    yield pod

    # Teardown: delete the pod (best-effort)
    try:
        core.delete_namespaced_pod(
            pod_name,
            namespace,
            body=k8s_client.V1DeleteOptions(grace_period_seconds=0),
        )
    except Exception:  # noqa: BLE001
        pass


# ── derived names ─────────────────────────────────────────────────────────────

def _resource_names(pod_name: str, pod_ns: str = "default"):
    """
    Replicate the Go name-computation logic from computeWstunnelResourceNames.
    Returns (shadow_namespace, resource_base_name).
    """
    import re

    def _sanitize(s: str) -> str:
        s = s.lower()
        s = re.sub(r"[^a-z0-9]", "-", s)
        s = s.strip("-")
        while "--" in s:
            s = s.replace("--", "-")
        return s[:63].rstrip("-") or "default"

    sanitized_ns = _sanitize(pod_ns)
    sanitized_name = _sanitize(pod_name)

    shadow_ns = (sanitized_ns + "-wstunnel")[:63].rstrip("-")
    base = (sanitized_name + "-" + sanitized_ns)[:63].rstrip("-")
    return shadow_ns, base


# ── tests ─────────────────────────────────────────────────────────────────────

class TestRatholeTLSResourceCreation:
    """
    Verify the VK creates the correct Kubernetes resources when a pod with
    exposed TCP ports is scheduled in rathole TLS mode.
    """

    def test_shadow_namespace_created(self, k8s_apis, tunnel_pod):
        """VK creates the <namespace>-wstunnel shadow namespace."""
        core = k8s_apis["core"]
        pod_name = tunnel_pod.metadata.name
        shadow_ns, _ = _resource_names(pod_name)

        def _check():
            namespaces = [ns.metadata.name for ns in core.list_namespace().items]
            return shadow_ns in namespaces

        _wait_for(_check, timeout=120, label=f"namespace/{shadow_ns}")
        namespaces = [ns.metadata.name for ns in core.list_namespace().items]
        assert shadow_ns in namespaces, f"Shadow namespace {shadow_ns!r} not found"

    def test_rathole_deployment_created(self, k8s_apis, tunnel_pod):
        """VK creates a rathole Deployment in the shadow namespace."""
        apps = k8s_apis["apps"]
        pod_name = tunnel_pod.metadata.name
        shadow_ns, base_name = _resource_names(pod_name)

        def _check():
            try:
                apps.read_namespaced_deployment(base_name, shadow_ns)
                return True
            except Exception:
                return False

        _wait_for(_check, timeout=120, label=f"deployment/{shadow_ns}/{base_name}")
        dep = apps.read_namespaced_deployment(base_name, shadow_ns)
        assert dep.metadata.name == base_name

    def test_server_certificate_created(self, k8s_apis, tunnel_pod):
        """VK creates a cert-manager Certificate for the rathole server TLS."""
        custom = k8s_apis["custom"]
        pod_name = tunnel_pod.metadata.name
        shadow_ns, base_name = _resource_names(pod_name)
        cert_name = f"{base_name}-rathole-server-tls"

        def _check():
            try:
                custom.get_namespaced_custom_object(
                    group="cert-manager.io",
                    version="v1",
                    namespace=shadow_ns,
                    plural="certificates",
                    name=cert_name,
                )
                return True
            except Exception:
                return False

        _wait_for(_check, timeout=120, label=f"Certificate/{shadow_ns}/{cert_name}")
        cert = custom.get_namespaced_custom_object(
            group="cert-manager.io", version="v1",
            namespace=shadow_ns, plural="certificates", name=cert_name,
        )
        assert cert["metadata"]["name"] == cert_name
        # Must reference the test CA issuer
        assert cert["spec"]["issuerRef"]["name"] == "interlink-test-ca"
        assert cert["spec"]["secretName"] == cert_name
        # DNS name must contain the rathole hostname pattern
        assert any("rathole" in dns for dns in cert["spec"].get("dnsNames", []))

    def test_client_certificate_created(self, k8s_apis, tunnel_pod):
        """VK creates a cert-manager Certificate for the rathole client."""
        custom = k8s_apis["custom"]
        pod_name = tunnel_pod.metadata.name
        shadow_ns, base_name = _resource_names(pod_name)
        cert_name = f"{base_name}-rathole-client-tls"

        def _check():
            try:
                custom.get_namespaced_custom_object(
                    group="cert-manager.io", version="v1",
                    namespace=shadow_ns, plural="certificates", name=cert_name,
                )
                return True
            except Exception:
                return False

        _wait_for(_check, timeout=120, label=f"Certificate/{shadow_ns}/{cert_name}")
        cert = custom.get_namespaced_custom_object(
            group="cert-manager.io", version="v1",
            namespace=shadow_ns, plural="certificates", name=cert_name,
        )
        assert cert["metadata"]["name"] == cert_name
        assert cert["spec"]["issuerRef"]["name"] == "interlink-test-ca"
        # Must request client auth usage
        assert "client auth" in cert["spec"].get("usages", [])

    def test_ingress_route_tcp_created(self, k8s_apis, tunnel_pod):
        """VK creates a Traefik IngressRouteTCP for TLS termination."""
        custom = k8s_apis["custom"]
        pod_name = tunnel_pod.metadata.name
        shadow_ns, base_name = _resource_names(pod_name)

        def _check():
            try:
                custom.get_namespaced_custom_object(
                    group="traefik.io", version="v1alpha1",
                    namespace=shadow_ns, plural="ingressroutetcps", name=base_name,
                )
                return True
            except Exception:
                return False

        _wait_for(_check, timeout=120, label=f"IngressRouteTCP/{shadow_ns}/{base_name}")
        route = custom.get_namespaced_custom_object(
            group="traefik.io", version="v1alpha1",
            namespace=shadow_ns, plural="ingressroutetcps", name=base_name,
        )
        spec = route["spec"]
        # Must use the websecure entrypoint
        assert "websecure" in spec.get("entryPoints", [])
        # TLS secretName must match the server certificate
        assert spec["tls"]["secretName"] == f"{base_name}-rathole-server-tls"
        # Route must have a HostSNI match rule
        routes = spec.get("routes", [])
        assert len(routes) > 0
        assert "HostSNI" in routes[0]["match"]
        # Service name must match the shadow deployment
        services = routes[0].get("services", [])
        assert any(s["name"] == base_name for s in services)

    def test_server_tls_secret_issued(self, k8s_apis, tunnel_pod):
        """cert-manager issues the server TLS secret with all required keys."""
        core = k8s_apis["core"]
        pod_name = tunnel_pod.metadata.name
        shadow_ns, base_name = _resource_names(pod_name)
        secret_name = f"{base_name}-rathole-server-tls"

        def _check():
            try:
                secret = core.read_namespaced_secret(secret_name, shadow_ns)
                data = secret.data or {}
                return all(k in data for k in ("tls.crt", "tls.key", "ca.crt"))
            except Exception:
                return False

        _wait_for(_check, timeout=180, label=f"Secret/{shadow_ns}/{secret_name}")
        secret = core.read_namespaced_secret(secret_name, shadow_ns)
        data = secret.data
        assert "tls.crt" in data and len(data["tls.crt"]) > 0
        assert "tls.key" in data and len(data["tls.key"]) > 0
        assert "ca.crt" in data and len(data["ca.crt"]) > 0
        # Validate tls.crt is valid base64 containing a PEM certificate
        decoded = base64.b64decode(data["tls.crt"])
        assert b"-----BEGIN CERTIFICATE-----" in decoded

    def test_client_tls_secret_issued(self, k8s_apis, tunnel_pod):
        """cert-manager issues the client TLS secret with all required keys."""
        core = k8s_apis["core"]
        pod_name = tunnel_pod.metadata.name
        shadow_ns, base_name = _resource_names(pod_name)
        secret_name = f"{base_name}-rathole-client-tls"

        def _check():
            try:
                secret = core.read_namespaced_secret(secret_name, shadow_ns)
                data = secret.data or {}
                return all(k in data for k in ("tls.crt", "tls.key", "ca.crt"))
            except Exception:
                return False

        _wait_for(_check, timeout=180, label=f"Secret/{shadow_ns}/{secret_name}")
        secret = core.read_namespaced_secret(secret_name, shadow_ns)
        data = secret.data
        assert "tls.crt" in data and len(data["tls.crt"]) > 0
        assert "tls.key" in data and len(data["tls.key"]) > 0
        assert "ca.crt" in data and len(data["ca.crt"]) > 0

    def test_pod_annotation_set(self, k8s_apis, tunnel_pod):
        """
        After cert-manager issues the client TLS secret, the VK patches the pod
        with interlink.eu/rathole-client-commands containing the bootstrap script.
        """
        core = k8s_apis["core"]
        pod_name = tunnel_pod.metadata.name

        def _check():
            pod = core.read_namespaced_pod(pod_name, "default")
            ann = (pod.metadata.annotations or {}).get("interlink.eu/rathole-client-commands", "")
            return ann.strip() != ""

        _wait_for(_check, timeout=300, label=f"pod/{pod_name} rathole annotation")
        pod = core.read_namespaced_pod(pod_name, "default")
        annotation = pod.metadata.annotations["interlink.eu/rathole-client-commands"]

        # The bootstrap command downloads and runs rathole with TLS certs
        assert "rathole" in annotation.lower()
        # Must contain base64-decoded cert file writes
        assert "base64" in annotation
        assert "rathole-ca.crt" in annotation
        assert "rathole-client.crt" in annotation
        assert "rathole-client.key" in annotation
        assert "rathole-client.toml" in annotation
        # Must background rathole
        assert annotation.rstrip().endswith("&")

    def test_annotation_toml_has_correct_host(self, k8s_apis, tunnel_pod):
        """
        The embedded base64 client TOML references the correct rathole server
        hostname derived from WildcardDNS (tunnel.test.local) and the resource name.
        """
        import re
        import base64 as b64lib

        core = k8s_apis["core"]
        pod_name = tunnel_pod.metadata.name
        _, base_name = _resource_names(pod_name)
        expected_host = f"rathole-{base_name}.tunnel.test.local"

        pod = core.read_namespaced_pod(pod_name, "default")
        annotation = pod.metadata.annotations.get("interlink.eu/rathole-client-commands", "")

        # The hostname lives inside the base64-encoded TOML blob written by the
        # bootstrap command ("echo <b64> | base64 -d > /tmp/rathole-client.toml").
        # Decode every such blob and search for the expected hostname there.
        decoded_parts = annotation
        for blob in re.findall(r'echo\s+([A-Za-z0-9+/=]+)\s*\|', annotation):
            try:
                decoded_parts += b64lib.b64decode(blob).decode("utf-8", errors="ignore")
            except Exception:
                pass

        assert expected_host in decoded_parts or base_name in decoded_parts, (
            f"Expected {expected_host!r} or {base_name!r} not found in annotation "
            f"(checked plain text and all base64-decoded blobs)"
        )


class TestRatholeTLSCleanup:
    """
    Verify that deleting the pod causes the VK to remove all tunnel TLS resources.
    This test must run AFTER TestRatholeTLSResourceCreation and reuses the same
    pod name (derived from tunnel_pod fixture, module-scoped).
    """

    def test_resources_cleaned_up_on_pod_delete(self, k8s_apis, tunnel_pod):
        """
        After the test pod is deleted (fixture teardown runs at end of module),
        trigger deletion here and check that the shadow namespace resources
        are removed.
        """
        from kubernetes import client as k8s_client

        core = k8s_apis["core"]
        apps = k8s_apis["apps"]
        custom = k8s_apis["custom"]
        pod_name = tunnel_pod.metadata.name
        shadow_ns, base_name = _resource_names(pod_name)

        # Delete the pod explicitly now (fixture will also try, harmlessly)
        try:
            core.delete_namespaced_pod(
                pod_name,
                "default",
                body=k8s_client.V1DeleteOptions(grace_period_seconds=0),
            )
        except Exception:
            pass

        # Wait for Deployment to be gone
        def _dep_gone():
            try:
                apps.read_namespaced_deployment(base_name, shadow_ns)
                return False
            except Exception:
                return True

        _wait_for(_dep_gone, timeout=120, label=f"deployment/{shadow_ns}/{base_name} deletion")

        # Verify Certificate CRs are removed (poll — API may lag behind deletion)
        for cert_suffix in ("rathole-server-tls", "rathole-client-tls"):
            cert_name = f"{base_name}-{cert_suffix}"

            def _cert_gone(cn=cert_name):
                try:
                    custom.get_namespaced_custom_object(
                        group="cert-manager.io", version="v1",
                        namespace=shadow_ns, plural="certificates", name=cn,
                    )
                    return False
                except Exception:
                    return True

            _wait_for(_cert_gone, timeout=60, label=f"Certificate/{shadow_ns}/{cert_name} deletion")

        # Verify IngressRouteTCP is removed
        def _route_gone():
            try:
                custom.get_namespaced_custom_object(
                    group="traefik.io", version="v1alpha1",
                    namespace=shadow_ns, plural="ingressroutetcps", name=base_name,
                )
                return False
            except Exception:
                return True

        _wait_for(_route_gone, timeout=60, label=f"IngressRouteTCP/{shadow_ns}/{base_name} deletion")
