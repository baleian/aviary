"""Environment variables passed to agent runtime containers."""

from app.config import settings


def build_task_env(agent_id: str) -> dict[str, str]:
    env = {
        "AGENT_ID": agent_id,
        "MAX_CONCURRENT_SESSIONS": str(settings.max_concurrent_sessions_per_task),
        "LLM_GATEWAY_URL": settings.llm_gateway_url,
        "LLM_GATEWAY_API_KEY": settings.llm_gateway_api_key,
        "MCP_GATEWAY_URL": settings.mcp_gateway_url,
    }
    return {k: v for k, v in env.items() if v}
