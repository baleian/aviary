"""Backend-agnostic idle cleanup loop (ported from v1)."""

import asyncio
import logging
from datetime import datetime, timezone, timedelta

from sqlalchemy import select
from sqlalchemy.orm import selectinload

from aviary_shared.db.models import Agent
from app.backends.protocol import RuntimeBackend
from app.config import settings
from app.db import async_session

logger = logging.getLogger(__name__)

_FAILURE_ESCALATION = 3


async def idle_cleanup_loop(backend: RuntimeBackend) -> None:
    failures = 0
    while True:
        await asyncio.sleep(settings.idle_cleanup_interval)
        try:
            cleaned = await _idle_cleanup(backend)
            if cleaned > 0:
                logger.info("Idle cleanup: scaled down %d agent(s)", cleaned)
            failures = 0
        except asyncio.CancelledError:
            return
        except Exception:
            failures += 1
            level = logging.ERROR if failures >= _FAILURE_ESCALATION else logging.WARNING
            logger.log(level, "Idle cleanup failed (%d consecutive)", failures, exc_info=True)


async def _idle_cleanup(backend: RuntimeBackend) -> int:
    cutoff = datetime.now(timezone.utc) - timedelta(seconds=settings.agent_idle_timeout)
    cleaned = 0

    async with async_session() as db:
        result = await db.execute(
            select(Agent)
            .where(Agent.status == "active")
            .options(selectinload(Agent.policy)),
        )
        agents = result.scalars().all()

    for agent in agents:
        agent_id = str(agent.id)

        replicas = await backend.list_replicas(agent_id)
        running = [r for r in replicas if r.status == "running"]
        if not running:
            continue

        last_activity = agent.policy.last_activity_at if agent.policy else None
        if last_activity and last_activity >= cutoff:
            continue

        stopped = await backend.stop_all_replicas(agent_id)
        if stopped:
            cleaned += 1
            logger.info(
                "Idle cleanup: stopped %d replica(s) for agent %s (last_activity=%s)",
                stopped, agent_id, last_activity,
            )

    return cleaned
