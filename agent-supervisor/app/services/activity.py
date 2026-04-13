"""Activity tracking — bumps `policies.last_activity_at` for an agent."""

import logging
import uuid
from datetime import datetime, timezone

from sqlalchemy import select

from aviary_shared.db.models import Agent, Policy
from app.db import async_session

logger = logging.getLogger(__name__)


async def touch_activity(agent_id: str) -> None:
    try:
        async with async_session() as session:
            agent = await session.get(Agent, uuid.UUID(agent_id))
            if agent is None:
                return

            result = await session.execute(select(Policy).where(Policy.agent_id == agent.id))
            policy = result.scalar_one_or_none()
            if policy is None:
                # Belt-and-suspenders: if somehow an agent was created without
                # a policy, create one now with safe defaults.
                policy = Policy(
                    agent_id=agent.id,
                    last_activity_at=datetime.now(timezone.utc),
                )
                session.add(policy)
            else:
                policy.last_activity_at = datetime.now(timezone.utc)

            await session.commit()
    except Exception as e:
        logger.warning("Failed to touch activity for agent %s: %s", agent_id, e)
