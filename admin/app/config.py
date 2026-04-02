from pydantic_settings import BaseSettings


class Settings(BaseSettings):
    database_url: str = "postgresql+asyncpg://aviary:aviary@localhost:5432/aviary"
    redis_url: str = "redis://localhost:6379/0"
    agent_controller_url: str = "http://localhost:9000"

    # Auto-scaling
    scaling_check_interval: int = 30
    sessions_per_pod_scale_up: int = 3
    sessions_per_pod_scale_down: int = 1

    # Idle cleanup
    default_agent_idle_timeout: int = 604800  # 7 days

    model_config = {"env_file": ".env", "env_file_encoding": "utf-8"}


settings = Settings()
