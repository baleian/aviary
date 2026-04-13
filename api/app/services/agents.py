"""Agent service — CRUD + supervisor lifecycle coordination."""

import uuid

from fastapi import HTTPException
from sqlalchemy import func, select
from sqlalchemy.ext.asyncio import AsyncSession

from app.schemas.agent import AgentCreate, AgentUpdate
from app.services.supervisor import supervisor_client
from aviary_shared.db.models import Agent, Policy, Session as SessionModel, User


async def list_for_owner(db: AsyncSession, user: User) -> list[Agent]:
    result = await db.execute(
        select(Agent)
        .where(Agent.owner_id == str(user.id), Agent.status != "deleted")
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

    policy = Policy(min_tasks=1, max_tasks=3)
    db.add(policy)
    await db.flush()

    agent = Agent(
        name=body.name,
        slug=body.slug,
        owner_id=str(user.id),
        instruction=body.instruction,
        model_config_data=body.model_config_data,
        tools=body.tools,
        policy_id=policy.id,
    )
    db.add(agent)
    await db.flush()

    # Lazy spawn: register storage only. Replicas are created on session
    # create (via /run) and released by idle_cleanup once activity ceases.
    await supervisor_client.register(str(agent.id))
    return agent


async def update(db: AsyncSession, agent: Agent, body: AgentUpdate) -> Agent:
    if body.name is not None:
        agent.name = body.name
    if body.instruction is not None:
        agent.instruction = body.instruction
    if body.model_config_data is not None:
        agent.model_config_data = body.model_config_data
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
    """Stop replicas, purge agent workspace, remove DB row + policy.
    Idempotent: safe to call multiple times."""
    agent_id_str = str(agent.id)
    policy_id = agent.policy_id

    await supervisor_client.delete(agent_id_str, purge=True)

    if policy_id is not None:
        policy = await db.get(Policy, policy_id)
        if policy is not None:
            await db.delete(policy)
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
