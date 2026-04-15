"""Configuration for agent-supervisor.

Role: streaming proxy + Redis publisher. No K8s CRUD, no backend abstraction.
Supervisor runs as a Pod in both local dev (K3s) and prod (EKS), so pool
Services are always reached via in-cluster DNS.

Stateless across replicas — the stream→runtime-pod mapping lives in Redis
so abort requests can land on any supervisor replica and still reach the
correct runtime pod directly.
"""

from pydantic_settings import BaseSettings


class Settings(BaseSettings):
    redis_url: str = "redis://redis:6379/0"

    agents_namespace: str = "agents"
    runtime_pool_port: int = 3000
    # Connect/request timeout for non-streaming calls (abort, cleanup).
    # The /v1/stream SSE loop itself has no overall timeout.
    runtime_pool_request_timeout: float = 30.0

    # How long a stream→runtime-pod Redis entry lives if the stream doesn't
    # clean up explicitly (pod crash). The normal path deletes the key at
    # stream completion.
    stream_runtime_ttl_seconds: int = 3600

    model_config = {"env_file": ".env", "env_file_encoding": "utf-8"}


settings = Settings()
