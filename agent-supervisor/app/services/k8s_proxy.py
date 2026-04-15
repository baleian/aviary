"""K8s API service-proxy client — used when the supervisor runs outside the cluster.

Local docker-compose puts the supervisor on the docker network while the pool
Services live inside K3s. In-cluster DNS (`env-default.agents.svc`) isn't
reachable from there, so we route through the K8s API server's built-in
service proxy using a ServiceAccount token.

URL form:
  {k8s_api_url}/api/v1/namespaces/{ns}/services/{service}:{port}/proxy/{path}

Paths and API URL are configurable (see config.py):
  - In-cluster (prod): defaults hit the auto-mounted SA token at
    /var/run/secrets/kubernetes.io/serviceaccount and use the implicit
    https://kubernetes.default.svc endpoint.
  - Local docker-compose: setup-dev.sh creates a supervisor SA, prints a
    long-lived token + the cluster CA into ./config/agent-supervisor/, and
    docker-compose bind-mounts that directory into the supervisor container.
    The K8S_API_URL env var points at the k3s container (`https://k8s:6443`).

Only imported when `runtime_pool_endpoint_mode == "k8s-proxy"`.
"""

from __future__ import annotations

from pathlib import Path

import httpx

from app.config import settings


def new_k8s_client(*, timeout: float | None = None) -> httpx.AsyncClient:
    token = Path(settings.k8s_token_path).read_text().strip()
    ca_path = Path(settings.k8s_ca_path)
    verify: str | bool = str(ca_path) if ca_path.exists() else False
    return httpx.AsyncClient(
        base_url=settings.k8s_api_url,
        headers={"Authorization": f"Bearer {token}"},
        verify=verify,
        timeout=timeout,
    )


def service_proxy_path(namespace: str, service: str, port: int, subpath: str) -> str:
    """Return the K8s API path that proxies to {service}:{port}/{subpath}."""
    subpath = subpath.lstrip("/")
    return f"/api/v1/namespaces/{namespace}/services/{service}:{port}/proxy/{subpath}"
