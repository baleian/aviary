"""Configuration for agent-supervisor.

Role: streaming proxy + Redis publisher. No K8s CRUD, no backend abstraction.
Pool endpoint resolution differs per deployment mode:

  direct-dns : supervisor runs inside the same cluster as the pools. Pool
               Services are reachable via in-cluster DNS
               `env-{pool}.{agents_namespace}.svc:{port}` (prod default).
  k8s-proxy  : supervisor runs outside the cluster (local docker-compose).
               Pool Services are reached through the K8s API server's
               service-proxy endpoint using the supervisor Pod's service
               account token (see services/k8s_proxy.py).
"""

from pydantic_settings import BaseSettings


class Settings(BaseSettings):
    redis_url: str = "redis://redis:6379/0"

    runtime_pool_endpoint_mode: str = "direct-dns"  # "direct-dns" | "k8s-proxy"
    agents_namespace: str = "agents"
    runtime_pool_port: int = 3000
    # Connect/request timeout for non-streaming calls (abort, cleanup).
    # The /v1/stream SSE loop itself has no overall timeout.
    runtime_pool_request_timeout: float = 30.0

    # K8s API connection (only used when endpoint mode is k8s-proxy).
    # Defaults match the in-pod convention; local docker-compose overrides these
    # to point at the k3s container and a bind-mounted token directory.
    k8s_api_url: str = "https://kubernetes.default.svc"
    k8s_token_path: str = "/var/run/secrets/kubernetes.io/serviceaccount/token"
    k8s_ca_path: str = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"

    model_config = {"env_file": ".env", "env_file_encoding": "utf-8"}


settings = Settings()
