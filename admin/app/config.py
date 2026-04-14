from pydantic_settings import BaseSettings


class Settings(BaseSettings):
    database_url: str = "postgresql+asyncpg://aviary:aviary@postgres:5432/aviary"
    supervisor_url: str = "http://agent-supervisor:9000"

    vault_addr: str = "http://vault:8200"
    vault_token: str = "dev-root-token"

    keycloak_url: str = "http://keycloak:8080"
    keycloak_admin: str = "admin"
    keycloak_password: str = "admin"
    keycloak_realm: str = "aviary"


settings = Settings()
