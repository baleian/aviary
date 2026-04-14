"""Backend-agnostic idle cleanup + orphan reconciliation loop."""

import asyncio
import logging
import uuid
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
            orphans = await _reconcile_orphans(backend)
            if orphans > 0:
                logger.warning("Reconciled %d orphan task(s)", orphans)
            failures = 0
        except asyncio.CancelledError:
            return
        except Exception:
            failures += 1
            level = logging.ERROR if failures >= _FAILURE_ESCALATION else logging.WARNING
            logger.log(level, "Cleanup loop failed (%d consecutive)", failures, exc_info=True)


async def _idle_cleanup(backend: RuntimeBackend) -> int:
    cutoff = datetime.now(timezone.utc) - timedelta(seconds=settings.agent_idle_timeout)
    cleaned = 0

    async with async_session() as db:
        result = await db.execute(
            select(Agent).options(selectinload(Agent.policy)),
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


async def _reconcile_orphans(backend: RuntimeBackend) -> int:
    """Stop tasks whose agent row has been deleted (or never existed).

    Runs from the task side — lists every managed task across the backend,
    then checks each unique agent_id against the DB. DB-read failure aborts
    the pass (treating every task as orphan would be catastrophic)."""
    tasks = await backend.list_all_replicas()
    if not tasks:
        return 0

    agent_ids: set[str] = {t.agent_id for t in tasks}

    async with async_session() as db:
        valid_uuids: list[uuid.UUID] = []
        for aid in agent_ids:
            try:
                valid_uuids.append(uuid.UUID(aid))
            except ValueError:
                continue
        known = set()
        if valid_uuids:
            result = await db.execute(select(Agent.id).where(Agent.id.in_(valid_uuids)))
            known = {str(row[0]) for row in result.all()}

    orphans = [t for t in tasks if t.agent_id not in known]
    stopped = 0
    cleaned_ids: set[str] = set()
    for task in orphans:
        if await backend.stop_replica(task):
            stopped += 1
        cleaned_ids.add(task.agent_id)

    # Volume subpath is DB-keyed too — reap it so EFS (prod) / local volume
    # doesn't accumulate forever.
    for aid in cleaned_ids:
        try:
            await backend.purge_agent_storage(aid)
        except Exception:
            logger.warning("purge_agent_storage failed for orphan %s", aid, exc_info=True)

    return stopped
