"""Environment variables passed to agent runtime containers."""

from app.config import settings


def build_task_env(agent_id: str) -> dict[str, str]:
    env = {
        "AGENT_ID": agent_id,
        "MAX_CONCURRENT_SESSIONS": str(settings.max_concurrent_sessions_per_task),
        "INFERENCE_ROUTER_URL": settings.inference_router_url,
        "ANTHROPIC_API_KEY": settings.litellm_api_key,
    }
    return {k: v for k, v in env.items() if v}
