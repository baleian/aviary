"""Agent service — CRUD + supervisor lifecycle coordination."""

import uuid

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.schemas.agent import AgentCreate, AgentUpdate
from app.services.supervisor import supervisor_client
from aviary_shared.db.models import Agent, User


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


async def require_owner(db: AsyncSession, agent_id: uuid.UUID, user: User) -> Agent:
    """Fetch agent and verify ownership. MVP: owner_id match only.
    Full RBAC comes in a later slice."""
    from fastapi import HTTPException

    agent = await get(db, agent_id)
    if not agent or agent.status == "deleted":
        raise HTTPException(404, "Agent not found")
    if agent.owner_id != str(user.id):
        raise HTTPException(403, "Not the agent owner")
    return agent


async def create(db: AsyncSession, user: User, body: AgentCreate) -> Agent:
    from fastapi import HTTPException

    existing = await db.execute(select(Agent).where(Agent.slug == body.slug))
    if existing.scalar_one_or_none():
        raise HTTPException(409, "Slug already exists")

    agent = Agent(
        name=body.name,
        slug=body.slug,
        owner_id=str(user.id),
        instruction=body.instruction,
        model_config_data=body.model_config_data,
        tools=body.tools,
    )
    db.add(agent)
    await db.flush()

    # Lazy spawn: register storage only. Replicas are created on first
    # message (run() in ws handler) and released by idle-cleanup when all
    # sessions go quiet.
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
    return agent


async def soft_delete(db: AsyncSession, agent: Agent) -> None:
    agent.status = "deleted"
    await db.flush()
    await supervisor_client.delete(str(agent.id))
