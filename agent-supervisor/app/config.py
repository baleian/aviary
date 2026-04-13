from pydantic_settings import BaseSettings


class Settings(BaseSettings):
    runtime_backend: str = "docker"
    runtime_image: str = "aviary-runtime:latest"
    runtime_port: int = 3000
    max_concurrent_sessions_per_task: int = 5

    docker_socket: str = "/var/run/docker.sock"
    # Network the supervisor itself lives on, inherited by agent containers
    # so they're reachable by container name. Auto-detected from the
    # supervisor's own container if left empty.
    docker_network: str = ""

    database_url: str = "postgresql+asyncpg://aviary:aviary@postgres:5432/aviary"
    redis_url: str = "redis://redis:6379/0"

    inference_router_url: str = ""

    scaling_check_interval: int = 30
    sessions_per_task_scale_up: int = 3
    sessions_per_task_scale_down: int = 1

    agent_idle_timeout: int = 604800
    idle_cleanup_interval: int = 300


settings = Settings()
