from pydantic_settings import BaseSettings


class Settings(BaseSettings):
    database_url: str
    oidc_issuer: str
    oidc_internal_issuer: str | None = None
    oidc_client_id: str
    oidc_audience: str | None = None
    vault_addr: str
    vault_token: str
    mcp_gateway_port: int = 8100
    platform_servers_config: str = "/app/config/platform-servers.yaml"
    secret_injection_config: str = "/app/config/secret-injection.yaml"

    model_config = {"env_file": ".env", "env_file_encoding": "utf-8"}


settings = Settings()
