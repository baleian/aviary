from pydantic_settings import BaseSettings


class Settings(BaseSettings):
    database_url: str = "postgresql+asyncpg://aviary:aviary@localhost:5432/aviary"
    agent_supervisor_url: str = "http://localhost:9000"

    # Vault
    vault_addr: str = "http://vault:8200"
    vault_token: str = "dev-root-token"

    # Keycloak Admin
    keycloak_url: str = "http://keycloak:8080"
    keycloak_admin: str = "admin"
    keycloak_admin_password: str = "admin"
    keycloak_realm: str = "aviary"

    model_config = {"env_file": ".env", "env_file_encoding": "utf-8"}


settings = Settings()
