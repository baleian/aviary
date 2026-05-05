from pydantic_settings import BaseSettings


class Settings(BaseSettings):
    # Secure-by-default. Set COOKIE_SECURE=false for non-localhost dev (LAN IP, host.docker.internal).
    cookie_secure: bool = True

    # Database
    database_url: str

    # Redis
    redis_url: str

    # OIDC — see .env.example.
    oidc_issuer: str
    oidc_internal_issuer: str | None = None
    oidc_client_id: str
    oidc_client_secret: str | None = None

    # CORS — single proxy origin (the browser never hits web directly).
    cors_origins: list[str] = ["http://localhost:3000"]

    # Agent Supervisor
    supervisor_url: str

    llm_gateway_url: str
    llm_gateway_api_key: str
    mcp_gateway_url: str
    mcp_gateway_api_key: str

    # Per-user credentials live at
    # secret/aviary/credentials/{sub}/{namespace}/{key_name}.
    vault_addr: str
    vault_token: str

    # Temporal — workflow orchestration
    temporal_host: str = "temporal:7233"
    temporal_namespace: str = "default"
    temporal_task_queue: str = "aviary-workflows"

    model_config = {"env_file": ".env", "env_file_encoding": "utf-8"}


settings = Settings()
