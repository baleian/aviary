"""Auto-scaling background task for agent deployments."""

import asyncio
import logging

from datetime import datetime, timezone

from sqlalchemy import select

from app.config import settings
from app.db import async_session_factory
from app.services import controller_client

logger = logging.getLogger(__name__)


async def scaling_loop() -> None:
    """Periodically check agent deployments and scale based on session load."""
    while True:
        await asyncio.sleep(settings.scaling_check_interval)
        try:
            await _check_and_scale()
        except Exception:
            logger.warning("Scaling check failed", exc_info=True)


async def _check_and_scale() -> None:
    from aviary_shared.db.models import Agent

    async with async_session_factory() as db:
        result = await db.execute(
            select(Agent).where(
                Agent.deployment_active.is_(True),
                Agent.status == "active",
            )
        )
        agents = result.scalars().all()

    for agent in agents:
        if not agent.namespace:
            continue
        try:
            await _scale_agent(agent)
        except Exception:
            logger.warning("Scaling failed for agent %s", agent.id, exc_info=True)


async def _scale_agent(agent) -> None:
    metrics = await controller_client.get_pod_metrics(agent.namespace)
    pods_queried = metrics.get("pods_queried", 0)
    if pods_queried == 0:
        return

    total_active = metrics.get("total_active", 0)
    sessions_per_pod = total_active / pods_queried

    status = await controller_client.get_deployment_status(agent.namespace)
    current_replicas = status.get("replicas", 1)

    if sessions_per_pod > settings.sessions_per_pod_scale_up and current_replicas < agent.max_pods:
        new_replicas = min(current_replicas + 1, agent.max_pods)
        await controller_client.scale_deployment(
            agent.namespace, new_replicas, agent.min_pods, agent.max_pods,
        )
        logger.info("Scaled up agent %s: %d → %d", agent.id, current_replicas, new_replicas)

    elif sessions_per_pod < settings.sessions_per_pod_scale_down and current_replicas > agent.min_pods:
        total_streaming = metrics.get("total_streaming", 0)
        if total_streaming > 0:
            return
        new_replicas = max(current_replicas - 1, agent.min_pods)
        await controller_client.scale_deployment(
            agent.namespace, new_replicas, agent.min_pods, agent.max_pods,
        )
        logger.info("Scaled down agent %s: %d → %d", agent.id, current_replicas, new_replicas)


async def cleanup_idle_agents() -> int:
    """Scale down deployments for agents idle longer than the timeout. Returns count."""
    from aviary_shared.db.models import Agent

    timeout_seconds = settings.default_agent_idle_timeout
    cutoff = datetime.now(timezone.utc).timestamp() - timeout_seconds

    async with async_session_factory() as db:
        result = await db.execute(
            select(Agent).where(
                Agent.deployment_active.is_(True),
                Agent.status == "active",
            )
        )
        agents = result.scalars().all()

        cleaned = 0
        for agent in agents:
            if agent.last_activity_at and agent.last_activity_at.timestamp() < cutoff:
                if agent.namespace:
                    try:
                        await controller_client.scale_to_zero(agent.namespace)
                    except Exception:
                        logger.warning("Failed to scale down agent %s", agent.id, exc_info=True)
                agent.deployment_active = False
                cleaned += 1

        if cleaned:
            await db.commit()
        return cleaned
