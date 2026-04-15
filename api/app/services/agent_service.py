"""Agent business logic: CRUD with ACL checks.

Infrastructure is managed declaratively by the infra team (pool manifests at
k8s/platform/pools/). An Agent is a DB row with a `pool_name` pointing at one
of those pools — no per-agent K8s resources exist.
"""

import uuid
import logging

from sqlalchemy import exists, func, or_, select
from sqlalchemy.ext.asyncio import AsyncSession

from app.db.models import Agent, Session, User
from app.schemas.agent import AgentCreate, AgentUpdate

logger = logging.getLogger(__name__)


async def create_agent(db: AsyncSession, user: User, data: AgentCreate) -> Agent:
    """Create a new agent row. Pool selection happens at this point; no infra calls."""
    existing = await db.execute(select(Agent).where(Agent.slug == data.slug))
    if existing.scalar_one_or_none():
        raise ValueError(f"Agent slug '{data.slug}' already exists")

    agent = Agent(
        name=data.name,
        slug=data.slug,
        description=data.description,
        owner_id=user.id,
        instruction=data.instruction,
        model_config_json=data.model_config_data.model_dump(),
        tools=data.tools,
        mcp_servers=[s.model_dump() for s in data.mcp_servers],
        visibility=data.visibility,
        category=data.category,
        icon=data.icon,
        pool_name=data.pool_name,
    )
    db.add(agent)
    await db.flush()
    return agent


async def get_agent(db: AsyncSession, agent_id: uuid.UUID) -> Agent | None:
    result = await db.execute(select(Agent).where(Agent.id == agent_id))
    return result.scalar_one_or_none()


async def get_agent_by_slug(db: AsyncSession, slug: str) -> Agent | None:
    result = await db.execute(select(Agent).where(Agent.slug == slug))
    return result.scalar_one_or_none()


async def list_agents_for_user(
    db: AsyncSession, user: User, offset: int = 0, limit: int = 50
) -> tuple[list[Agent], int]:
    """List agents visible to a user based on ACL + visibility rules."""
    from app.db.models import AgentACL, TeamMember

    team_ids_result = await db.execute(
        select(TeamMember.team_id).where(TeamMember.user_id == user.id)
    )
    user_team_ids = [row[0] for row in team_ids_result.all()]

    conditions = [
        Agent.owner_id == user.id,
        Agent.visibility == "public",
        exists(
            select(AgentACL.id).where(
                AgentACL.agent_id == Agent.id,
                AgentACL.user_id == user.id,
            )
        ),
    ]
    if user_team_ids:
        conditions.append(
            exists(
                select(AgentACL.id).where(
                    AgentACL.agent_id == Agent.id,
                    AgentACL.team_id.in_(user_team_ids),
                )
            )
        )
        conditions.append(Agent.visibility == "team")

    base_query = select(Agent).where(or_(*conditions))

    count_result = await db.execute(
        select(func.count()).select_from(base_query.subquery())
    )
    total = count_result.scalar() or 0

    result = await db.execute(
        base_query.order_by(Agent.created_at.desc()).offset(offset).limit(limit)
    )
    return list(result.scalars().all()), total


async def update_agent(
    db: AsyncSession, agent: Agent, data: AgentUpdate
) -> Agent:
    if data.name is not None:
        agent.name = data.name
    if data.description is not None:
        agent.description = data.description
    if data.instruction is not None:
        agent.instruction = data.instruction
    if data.model_config_data is not None:
        agent.model_config_json = data.model_config_data.model_dump()
    if data.tools is not None:
        agent.tools = data.tools
    if data.mcp_servers is not None:
        agent.mcp_servers = [s.model_dump() for s in data.mcp_servers]
    if data.visibility is not None:
        agent.visibility = data.visibility
    if data.category is not None:
        agent.category = data.category
    if data.icon is not None:
        agent.icon = data.icon
    if data.pool_name is not None:
        agent.pool_name = data.pool_name

    await db.flush()
    return agent


async def delete_agent(db: AsyncSession, agent: Agent) -> None:
    """Hard-delete an agent row. Active sessions cascade-delete via FK."""
    await db.delete(agent)
    await db.flush()
