from pydantic_settings import BaseSettings


class Settings(BaseSettings):
    database_url: str = "postgresql+asyncpg://aviary:aviary@postgres:5432/aviary"
    redis_url: str = "redis://redis:6379/0"

    supervisor_url: str = "http://agent-supervisor:9000"

    oidc_issuer: str = "http://localhost:8080/realms/aviary"
    oidc_internal_issuer: str = "http://keycloak:8080/realms/aviary"
    oidc_client_id: str = "aviary-web"
    oidc_audience: str | None = None

    cors_origins: list[str] = ["http://localhost:3000"]
    cookie_secure: bool = False

    agent_ready_timeout: int = 90

    vault_addr: str = "http://vault:8200"
    vault_token: str = "dev-root-token"


settings = Settings()
