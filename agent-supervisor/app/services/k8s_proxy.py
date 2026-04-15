"""K8s API service-proxy client — used when the supervisor runs outside the cluster.

Local docker-compose puts the supervisor on the docker network while the pool
Services live inside K3s. In-cluster DNS (`env-default.agents.svc`) isn't
reachable from there, so we route through the K8s API server's built-in
service proxy using the supervisor's ServiceAccount token.

URL form:
  https://kubernetes.default.svc
    /api/v1/namespaces/{ns}/services/{service}:{port}/proxy/{path}

This module exists only for `runtime_pool_endpoint_mode == "k8s-proxy"`.
In-cluster deployments use `direct-dns` and never import it.
"""

from __future__ import annotations

from pathlib import Path

import httpx

_TOKEN_PATH = Path("/var/run/secrets/kubernetes.io/serviceaccount/token")
_CA_PATH = Path("/var/run/secrets/kubernetes.io/serviceaccount/ca.crt")
_K8S_API = "https://kubernetes.default.svc"


def new_k8s_client(*, timeout: float | None = None) -> httpx.AsyncClient:
    token = _TOKEN_PATH.read_text()
    ca = str(_CA_PATH) if _CA_PATH.exists() else False
    return httpx.AsyncClient(
        base_url=_K8S_API,
        headers={"Authorization": f"Bearer {token}"},
        verify=ca,
        timeout=timeout,
    )


def service_proxy_path(namespace: str, service: str, port: int, subpath: str) -> str:
    """Return the K8s API path that proxies to {service}:{port}/{subpath}."""
    subpath = subpath.lstrip("/")
    return f"/api/v1/namespaces/{namespace}/services/{service}:{port}/proxy/{subpath}"
