"""Agent-centric API endpoints."""

import logging

from fastapi import APIRouter, Query, Request
from fastapi.responses import JSONResponse, StreamingResponse

from sqlalchemy import select
from sqlalchemy.orm import selectinload

from aviary_shared.db.models import Agent
from app.config import settings
from app.db import async_session
from app.services.activity import touch_activity
from app.services.network_policy import extract_network_policy
from app.services.runtime_env import build_task_env
from app.services.streaming import proxy_and_publish

router = APIRouter(prefix="/v1")
logger = logging.getLogger(__name__)


def _get_backend(request: Request):
    return request.app.state.backend


def _get_redis(request: Request):
    return request.app.state.redis_publisher


async def _agent_scaling_spec(agent_id: str) -> tuple[int, dict | None]:
    async with async_session() as db:
        result = await db.execute(
            select(Agent).where(Agent.id == agent_id).options(selectinload(Agent.policy)),
        )
        agent = result.scalar_one_or_none()
    if agent and agent.policy:
        return max(1, agent.policy.min_tasks), extract_network_policy(agent.policy)
    return 1, None


@router.post("/agents/{agent_id}/register")
async def register_agent(agent_id: str, request: Request):
    backend = _get_backend(request)
    volumes = await backend.ensure_storage(agent_id)
    await touch_activity(agent_id)
    return {"status": "registered", "agent_id": agent_id, "volumes": volumes}


@router.delete("/agents/{agent_id}")
async def delete_agent(
    agent_id: str,
    request: Request,
    purge: bool = Query(default=False, description="Also purge agent workspace storage"),
):
    backend = _get_backend(request)
    stopped = await backend.stop_all_replicas(agent_id)
    if purge:
        await backend.purge_agent_storage(agent_id)
    return {
        "status": "deleted",
        "agent_id": agent_id,
        "replicas_stopped": stopped,
        "purged": purge,
    }


@router.post("/agents/{agent_id}/run")
async def run_agent(agent_id: str, request: Request):
    """Ensure at least min_replicas running for this agent."""
    backend = _get_backend(request)
    target, network_policy = await _agent_scaling_spec(agent_id)
    actual = await backend.scale_to(
        agent_id, target, settings.runtime_image, build_task_env(agent_id),
        network_policy=network_policy,
    )
    await touch_activity(agent_id)
    return {
        "status": "running", "agent_id": agent_id, "replicas": actual,
        "network_policy": network_policy,
    }


@router.post("/agents/{agent_id}/restart")
async def restart_agent(agent_id: str, request: Request):
    """Rolling restart — stop all replicas, then respawn min_replicas.

    Stateless respawn is acceptable because `.claude/{session_id}/` state
    lives on the shared workspace volume; any fresh replica can resume.
    """
    backend = _get_backend(request)
    stopped = await backend.stop_all_replicas(agent_id)
    target, network_policy = await _agent_scaling_spec(agent_id)
    actual = await backend.scale_to(
        agent_id, target, settings.runtime_image, build_task_env(agent_id),
        network_policy=network_policy,
    )
    await touch_activity(agent_id)
    return {
        "status": "restarted", "agent_id": agent_id,
        "replicas_stopped": stopped, "replicas": actual,
    }


@router.post("/agents/{agent_id}/scale")
async def scale_agent(
    agent_id: str,
    request: Request,
    target: int = Query(..., ge=0, le=32, description="Desired replica count"),
):
    """Admin-override scaling. Bypasses min/max_tasks from policy — the
    auto-scaler will still be free to adjust later."""
    backend = _get_backend(request)
    _, network_policy = await _agent_scaling_spec(agent_id)
    actual = await backend.scale_to(
        agent_id, target, settings.runtime_image, build_task_env(agent_id),
        network_policy=network_policy,
    )
    return {"status": "scaled", "agent_id": agent_id, "target": target, "replicas": actual}


@router.get("/agents/{agent_id}/replicas")
async def list_replicas(agent_id: str, request: Request):
    backend = _get_backend(request)
    replicas = await backend.list_replicas(agent_id)
    return {
        "agent_id": agent_id,
        "replicas": [
            {"replica": r.replica, "task_id": r.task_id, "host": r.host, "status": r.status}
            for r in replicas
        ],
    }


@router.get("/agents/{agent_id}/ready")
async def agent_ready(agent_id: str, request: Request):
    backend = _get_backend(request)
    replicas = await backend.list_replicas(agent_id)
    running = [r for r in replicas if r.status == "running"]
    if not running:
        return JSONResponse(status_code=404, content={"error": "no running replicas"})
    return {
        "agent_id": agent_id,
        "ready": len(running) > 0,
        "replicas_total": len(replicas),
        "replicas_running": len(running),
    }


@router.get("/agents/{agent_id}/wait")
async def wait_for_agent(
    agent_id: str,
    request: Request,
    timeout: int = Query(default=90, le=300),
):
    backend = _get_backend(request)
    replicas = await backend.list_replicas(agent_id)
    if not replicas:
        return JSONResponse(status_code=404, content={"error": "no replicas"})

    # Wait until at least one replica is ready.
    for r in replicas:
        if await backend.wait_for_ready(r, timeout=timeout):
            return {"status": "ready", "agent_id": agent_id, "replica": r.replica}
    return JSONResponse(status_code=408, content={"error": "timeout waiting for agent"})


@router.post("/agents/{agent_id}/sessions/{session_id}/message")
async def send_message(agent_id: str, session_id: str, request: Request):
    backend = _get_backend(request)
    redis_pub = _get_redis(request)
    body = await request.json()

    await touch_activity(agent_id)

    return StreamingResponse(
        proxy_and_publish(backend, redis_pub, agent_id, session_id, body),
        media_type="text/event-stream",
        headers={"Cache-Control": "no-cache", "X-Accel-Buffering": "no"},
    )


@router.post("/agents/{agent_id}/sessions/{session_id}/abort")
async def abort_session(agent_id: str, session_id: str, request: Request):
    backend = _get_backend(request)
    ok = await backend.abort_session(agent_id, session_id)
    return {"ok": ok, "agent_id": agent_id, "session_id": session_id}


@router.delete("/agents/{agent_id}/sessions/{session_id}")
async def cleanup_session(agent_id: str, session_id: str, request: Request):
    backend = _get_backend(request)
    result = await backend.cleanup_session(agent_id, session_id)
    return result


@router.get("/agents/{agent_id}/metrics")
async def agent_metrics(agent_id: str, request: Request):
    """Aggregated metrics across all replicas."""
    backend = _get_backend(request)
    replicas = await backend.list_replicas(agent_id)
    if not replicas:
        return JSONResponse(status_code=404, content={"error": "no replicas"})

    total_active = 0
    total_streaming = 0
    total_max = 0
    per_replica = []
    for r in replicas:
        m = await backend.get_replica_metrics(r)
        if m is None:
            per_replica.append({"replica": r.replica, "status": r.status, "metrics": None})
            continue
        total_active += m.sessions_active
        total_streaming += m.sessions_streaming
        total_max += m.sessions_max
        per_replica.append({
            "replica": r.replica,
            "status": r.status,
            "metrics": {
                "sessions_active": m.sessions_active,
                "sessions_streaming": m.sessions_streaming,
                "sessions_max": m.sessions_max,
            },
        })

    return {
        "agent_id": agent_id,
        "replicas": per_replica,
        "total": {
            "sessions_active": total_active,
            "sessions_streaming": total_streaming,
            "sessions_max": total_max,
        },
    }


@router.get("/health")
async def health(request: Request):
    backend = _get_backend(request)
    return await backend.health_check()
