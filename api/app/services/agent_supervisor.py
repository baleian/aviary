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
    stream_id: str,
    body: dict,
) -> dict:
    """Run one agent turn end-to-end on the given pool.

    `stream_id` is a caller-allocated UUID that identifies this specific turn
    for later abort routing (supervisor maps stream_id → owning Pod in Redis).
    Blocks until the runtime stream closes.
    """
    resp = await _supervisor.client.post(
        "/v1/stream",
        json={
            "pool_name": pool_name,
            "session_id": session_id,
            "agent_id": agent_id,
            "stream_id": stream_id,
            "body": body,
        },
        timeout=None,
    )
    resp.raise_for_status()
    return resp.json()


async def abort_stream(*, stream_id: str, session_id: str) -> None:
    """Best-effort abort. Any supervisor replica can service this — the
    replica looks up the runtime pod in Redis and POSTs directly to that
    pod's /abort/{session_id}, bypassing the pool Service LB.
    """
    try:
        resp = await _supervisor.client.post(
            f"/v1/streams/{stream_id}/abort",
            json={"session_id": session_id},
            timeout=5,
        )
        resp.raise_for_status()
    except httpx.HTTPError:
        logger.warning("Failed to abort stream %s", stream_id, exc_info=True)


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
