"""Activity tracking — updates last_activity_at in DB."""

import logging
from datetime import datetime, timezone

from sqlalchemy import select

from aviary_shared.db.models import Agent, Policy
from app.db import async_session

logger = logging.getLogger(__name__)


async def touch_activity(agent_id: str) -> None:
    try:
        async with async_session() as session:
            agent = await session.get(Agent, agent_id)
            if agent is None:
                return

            if agent.policy_id is None:
                policy = Policy(last_activity_at=datetime.now(timezone.utc))
                session.add(policy)
                await session.flush()
                agent.policy_id = policy.id
            else:
                policy = await session.get(Policy, agent.policy_id)
                if policy:
                    policy.last_activity_at = datetime.now(timezone.utc)

            await session.commit()
    except Exception as e:
        logger.warning("Failed to touch activity for agent %s: %s", agent_id, e)
