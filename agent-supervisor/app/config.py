"""Configuration for agent-supervisor.

Role: streaming proxy + Redis publisher. No K8s CRUD, no backend abstraction.
Supervisor runs as a Pod in both local dev (K3s) and prod (EKS), so pool
Services are always reached via in-cluster DNS.
"""

from pydantic_settings import BaseSettings


class Settings(BaseSettings):
    redis_url: str = "redis://redis:6379/0"

    agents_namespace: str = "agents"
    runtime_pool_port: int = 3000
    # Connect/request timeout for non-streaming calls (cleanup).
    # The /v1/stream SSE loop itself has no overall timeout.
    runtime_pool_request_timeout: float = 30.0

    # Pod identity (Downward API). Used for debugging and future pod-sticky
    # abort routing via Redis.
    pod_name: str = "local"

    model_config = {"env_file": ".env", "env_file_encoding": "utf-8"}


settings = Settings()
