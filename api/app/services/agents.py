"""Agent service — CRUD + supervisor lifecycle coordination."""

import uuid

from fastapi import HTTPException
from sqlalchemy import exists, func, or_, select
from sqlalchemy.ext.asyncio import AsyncSession

from app.schemas.agent import AgentCreate, AgentUpdate
from app.services.supervisor import supervisor_client
from aviary_shared.db.models import Agent, Policy, Session as SessionModel, User  # noqa: F401


async def list_for_owner(db: AsyncSession, user: User) -> list[Agent]:
    # Soft-deleted agents that still have sessions stay visible so the owner
    # can finish ongoing conversations (new sessions and edits are blocked
    # by require_owner_active). They disappear once the last session is gone
    # via the cascade in sessions.delete.
    visible = or_(
        Agent.status != "deleted",
        exists(select(SessionModel.id).where(SessionModel.agent_id == Agent.id)),
    )
    result = await db.execute(
        select(Agent)
        .where(Agent.owner_id == str(user.id), visible)
        .order_by(Agent.created_at.desc())
    )
    return list(result.scalars().all())


async def get(db: AsyncSession, agent_id: uuid.UUID) -> Agent | None:
    result = await db.execute(select(Agent).where(Agent.id == agent_id))
    return result.scalar_one_or_none()


async def _require_owner(
    db: AsyncSession, agent_id: uuid.UUID, user: User, *, include_deleted: bool,
) -> Agent:
    agent = await get(db, agent_id)
    if not agent or (not include_deleted and agent.status == "deleted"):
        raise HTTPException(404, "Agent not found")
    if agent.owner_id != str(user.id):
        raise HTTPException(403, "Not the agent owner")
    return agent


async def require_owner_active(db: AsyncSession, agent_id: uuid.UUID, user: User) -> Agent:
    """For mutating ops (new session, PATCH) — rejects soft-deleted agents."""
    return await _require_owner(db, agent_id, user, include_deleted=False)


async def require_owner_viewable(db: AsyncSession, agent_id: uuid.UUID, user: User) -> Agent:
    """For read-only / idempotent-delete ops — accepts soft-deleted agents so
    the detail page remains browsable while orphan sessions drain."""
    return await _require_owner(db, agent_id, user, include_deleted=True)


async def create(db: AsyncSession, user: User, body: AgentCreate) -> Agent:
    existing = await db.execute(select(Agent).where(Agent.slug == body.slug))
    if existing.scalar_one_or_none():
        raise HTTPException(409, "Slug already exists")

    agent = Agent(
        name=body.name,
        slug=body.slug,
        owner_id=str(user.id),
        description=body.description,
        instruction=body.instruction,
        model_config_data=body.model_config_data.model_dump(),
        tools=body.tools,
    )
    db.add(agent)
    await db.flush()

    # Default policy: [1, 3]. Zero-scale below 1 is driven by idle_cleanup,
    # not scaling — keeps behavior predictable during active conversations.
    db.add(Policy(agent_id=agent.id, min_tasks=1, max_tasks=3))
    await db.flush()

    # Lazy spawn: register storage only. Replicas are created on session
    # create (via /run) and released by idle_cleanup once activity ceases.
    await supervisor_client.register(str(agent.id))
    return agent


async def update(db: AsyncSession, agent: Agent, body: AgentUpdate) -> Agent:
    if body.name is not None:
        agent.name = body.name
    if body.description is not None:
        agent.description = body.description
    if body.instruction is not None:
        agent.instruction = body.instruction
    if body.model_config_data is not None:
        agent.model_config_data = body.model_config_data.model_dump()
    if body.tools is not None:
        agent.tools = body.tools
    await db.flush()
    await db.refresh(agent)
    return agent


async def session_count(db: AsyncSession, agent_id: uuid.UUID) -> int:
    result = await db.execute(
        select(func.count(SessionModel.id)).where(SessionModel.agent_id == agent_id)
    )
    return int(result.scalar_one())


async def hard_delete(db: AsyncSession, agent: Agent) -> None:
    """Stop replicas, purge agent workspace, remove the agent row.

    FK CASCADE propagates the row delete to policy, sessions, and messages
    in a single statement — nothing else to clean up at the DB level.
    """
    await supervisor_client.delete(str(agent.id), purge=True)
    await db.delete(agent)
    await db.flush()


async def soft_delete(db: AsyncSession, agent: Agent) -> None:
    """Mark the agent deleted; keep resources alive for orphan sessions.

    If there are no active sessions, proceed straight to hard-delete — no
    reason to keep a skeleton around.
    """
    agent.status = "deleted"
    await db.flush()
    if await session_count(db, agent.id) == 0:
        await hard_delete(db, agent)
