"""Backend-agnostic auto-scaling loop (ported from v1)."""

import asyncio
import logging

from sqlalchemy import select
from sqlalchemy.orm import selectinload

from aviary_shared.db.models import Agent
from app.backends.protocol import RuntimeBackend
from app.config import settings
from app.db import async_session
from app.services.network_policy import extract_network_policy
from app.services.runtime_env import build_task_env

logger = logging.getLogger(__name__)

_DEFAULT_MIN = 1
_DEFAULT_MAX = 3
_FAILURE_ESCALATION = 3


async def scaling_loop(backend: RuntimeBackend) -> None:
    failures = 0
    while True:
        await asyncio.sleep(settings.scaling_check_interval)
        try:
            await _check_and_scale(backend)
            failures = 0
        except asyncio.CancelledError:
            return
        except Exception:
            failures += 1
            level = logging.ERROR if failures >= _FAILURE_ESCALATION else logging.WARNING
            logger.log(level, "Scaling check failed (%d consecutive)", failures, exc_info=True)


async def _check_and_scale(backend: RuntimeBackend) -> None:
    async with async_session() as db:
        result = await db.execute(
            select(Agent).options(selectinload(Agent.policy)),
        )
        agents = result.scalars().all()

    for agent in agents:
        agent_id = str(agent.id)
        min_replicas = agent.policy.min_tasks if agent.policy else _DEFAULT_MIN
        max_replicas = agent.policy.max_tasks if agent.policy else _DEFAULT_MAX
        network_policy = extract_network_policy(agent.policy)

        try:
            await _scale_agent(backend, agent_id, min_replicas, max_replicas, network_policy)
        except Exception:
            logger.warning("Scaling failed for agent %s", agent_id, exc_info=True)


async def _scale_agent(
    backend: RuntimeBackend,
    agent_id: str,
    min_replicas: int,
    max_replicas: int,
    network_policy: dict | None = None,
) -> None:
    replicas = await backend.list_replicas(agent_id)
    running = [r for r in replicas if r.status == "running"]

    # Agent not started yet — skip (autoscale doesn't bring idle agents up)
    if not running:
        return

    total_active = 0
    total_streaming = 0
    queried = 0
    for r in running:
        m = await backend.get_replica_metrics(r)
        if m is None:
            continue
        total_active += m.sessions_active
        total_streaming += m.sessions_streaming
        queried += 1

    if queried == 0:
        logger.error("No replica metrics for agent %s — skipping scale decision", agent_id)
        return

    sessions_per_replica = total_active / queried
    current = len(running)

    if sessions_per_replica > settings.sessions_per_task_scale_up and current < max_replicas:
        new_count = min(current + 1, max_replicas)
        actual = await backend.scale_to(
            agent_id, new_count, settings.runtime_image, build_task_env(agent_id),
            network_policy=network_policy,
        )
        logger.info("Scaled up agent %s: %d → %d", agent_id, current, actual)

    elif sessions_per_replica < settings.sessions_per_task_scale_down and current > min_replicas:
        if total_streaming > 0:
            return  # don't scale down while streams are in flight
        new_count = max(current - 1, min_replicas)
        actual = await backend.scale_to(
            agent_id, new_count, settings.runtime_image, build_task_env(agent_id),
            network_policy=network_policy,
        )
        logger.info("Scaled down agent %s: %d → %d", agent_id, current, actual)
