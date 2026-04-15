"""Agent Supervisor client — pool-based streaming proxy.

The API server never touches K8s. It forwards (pool_name, session_id,
agent_id, body) to agent-supervisor, which streams from the pool Service and
publishes events to Redis. WS clients get live events via Redis subscribe.
"""

import logging

import httpx

from aviary_shared.http import ServiceClient

from app.config import settings

logger = logging.getLogger(__name__)

_supervisor = ServiceClient(base_url=settings.agent_supervisor_url)


async def init_client() -> None:
    await _supervisor.init()


async def close_client() -> None:
    await _supervisor.close()


async def stream_message(
    *,
    pool_name: str,
    session_id: str,
    agent_id: str,
    body: dict,
) -> dict:
    """Run one agent turn end-to-end on the given pool.

    Blocks until the runtime stream closes. Returns
    `{status: 'complete'|'error', reached_runtime: bool, result_meta?: dict,
      message?: str}`. Callers consume events via the shared Redis
    `session:{id}:messages` channel.
    """
    resp = await _supervisor.client.post(
        "/v1/stream",
        json={
            "pool_name": pool_name,
            "session_id": session_id,
            "agent_id": agent_id,
            "body": body,
        },
        timeout=None,
    )
    resp.raise_for_status()
    return resp.json()


async def abort_session(*, pool_name: str, session_id: str) -> None:
    """Best-effort abort — races with normal completion, so failures are logged only."""
    try:
        resp = await _supervisor.client.post(
            f"/v1/sessions/{session_id}/abort",
            json={"pool_name": pool_name},
            timeout=5,
        )
        resp.raise_for_status()
    except httpx.HTTPError:
        logger.warning("Failed to abort session %s on pool %s", session_id, pool_name, exc_info=True)


async def cleanup_session(*, pool_name: str, session_id: str, agent_id: str) -> None:
    """Best-effort workspace subdirectory cleanup on the pool pod."""
    try:
        resp = await _supervisor.client.delete(
            f"/v1/sessions/{session_id}",
            params={"pool_name": pool_name, "agent_id": agent_id},
            timeout=10,
        )
        resp.raise_for_status()
    except httpx.HTTPError:
        logger.warning(
            "Session cleanup failed for %s on pool %s", session_id, pool_name, exc_info=True,
        )


async def health_check() -> bool:
    try:
        resp = await _supervisor.client.get("/health")
        return resp.status_code == 200
    except httpx.HTTPError:
        logger.debug("Supervisor health check failed", exc_info=True)
        return False
