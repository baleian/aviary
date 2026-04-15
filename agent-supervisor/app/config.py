from pydantic_settings import BaseSettings


class Settings(BaseSettings):
    # Backend selection
    backend_kind: str = "k3s"  # k3s | eks_native | eks_fargate

    # Agent runtime defaults
    agent_runtime_image: str = "aviary-runtime:latest"
    agent_image_pull_policy: str = "Never"  # k3s: Never; eks_native: IfNotPresent
    max_concurrent_sessions_per_pod: int = 5
    host_gateway_ip: str = ""  # k3s-only; EKS leaves blank

    # EKS-native backend config (ignored when backend_kind=k3s)
    efs_storage_class: str = "efs-sc"
    efs_shared_workspace_pvc: str = "shared-workspace"
    eks_baseline_security_group_ids: str = ""  # comma-separated AWS sg-ids
    eks_irsa_role_arn: str = ""  # optional per-SA IRSA role

    # Fargate-specific (ignored outside eks_fargate). 0 = keep default (20 GiB).
    fargate_ephemeral_storage_gib: int = 0
    default_memory_limit: str = "4Gi"
    default_cpu_limit: str = "4"
    default_min_pods: int = 0
    default_max_pods: int = 3

    # A session is "active" for scaling purposes only if its last message is
    # newer than this threshold. Older sessions are treated as idle and
    # excluded from KEDA's active-session count so pods can scale down.
    session_idle_threshold_seconds: int = 300

    # Database
    database_url: str

    # Redis (for stream buffer + pub/sub shared with API server)
    redis_url: str = "redis://redis:6379/0"

    # Pod environment
    inference_router_url: str
    mcp_gateway_url: str
    litellm_api_key: str
    aviary_api_url: str
    internal_api_key: str

    model_config = {"env_file": ".env", "env_file_encoding": "utf-8"}

    @property
    def eks_baseline_sg_list(self) -> list[str]:
        return [s.strip() for s in self.eks_baseline_security_group_ids.split(",") if s.strip()]


settings = Settings()
