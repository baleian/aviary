"""Agent CRUD — owner-only."""

import logging
import uuid

from sqlalchemy import func, select
from sqlalchemy.ext.asyncio import AsyncSession

from app.db.models import (
    Agent, AgentVersion, CatalogAgent, CatalogImport, Session, User,
)
from app.schemas.agent import AgentCreate, AgentResponse, AgentUpdate
from app.errors import ConflictError
from app.services.catalog_permissions import is_catalog_editor

logger = logging.getLogger(__name__)


async def build_agent_response(db: AsyncSession, agent: Agent) -> AgentResponse:
    """Build an AgentResponse with display fields hydrated from the catalog
    snapshot for imported agents, plus derived flags (editor, deprecated,
    pinned_version_id).

    Plan §1.2: imported agents' local fields are placeholders — display
    always reads the effective version snapshot. This is the single choke
    point that enforces that rule for every API response.
    """
    display_name = agent.name
    description = agent.description
    icon = agent.icon
    instruction = agent.instruction
    model_config_json = dict(agent.model_config_json or {})
    tools = list(agent.tools or [])
    mcp_servers = list(agent.mcp_servers or [])

    editor = False
    deprecated = False
    pinned_version_id: uuid.UUID | None = None

    if agent.linked_catalog_agent_id is not None:
        ca = (await db.execute(
            select(CatalogAgent).where(CatalogAgent.id == agent.linked_catalog_agent_id)
        )).scalar_one_or_none()
        if ca is not None:
            editor = is_catalog_editor(agent, ca)

    if agent.catalog_import_id is not None:
        ci = (await db.execute(
            select(CatalogImport).where(CatalogImport.id == agent.catalog_import_id)
        )).scalar_one_or_none()
        if ci is not None:
            pinned_version_id = ci.pinned_version_id
            ca_for_import = (await db.execute(
                select(CatalogAgent).where(CatalogAgent.id == ci.catalog_agent_id)
            )).scalar_one_or_none()
            effective_id = ci.pinned_version_id or (
                ca_for_import.current_version_id if ca_for_import else None
            )
            version = None
            if effective_id is not None:
                version = (await db.execute(
                    select(AgentVersion).where(AgentVersion.id == effective_id)
                )).scalar_one_or_none()
            if version is not None:
                display_name = version.name
                description = version.description
                icon = version.icon
                instruction = version.instruction
                model_config_json = dict(version.model_config_json or {})
                tools = list(version.tools or [])
                mcp_servers = list(version.mcp_servers or [])
                deprecated = (
                    (ca_for_import is not None and ca_for_import.unpublished_at is not None)
                    or version.unpublished_at is not None
                )
            elif ca_for_import is not None:
                # No effective version (catalog has none left) — still flag deprecated.
                deprecated = ca_for_import.unpublished_at is not None

    return AgentResponse(
        id=str(agent.id),
        name=display_name,
        slug=agent.slug,
        description=description,
        owner_id=str(agent.owner_id),
        instruction=instruction,
        model_config_json=model_config_json,
        tools=tools,
        mcp_servers=mcp_servers,
        icon=icon,
        runtime_endpoint=agent.runtime_endpoint,
        status=agent.status,
        created_at=agent.created_at,
        updated_at=agent.updated_at,
        linked_catalog_agent_id=(
            str(agent.linked_catalog_agent_id) if agent.linked_catalog_agent_id else None
        ),
        catalog_import_id=(
            str(agent.catalog_import_id) if agent.catalog_import_id else None
        ),
        is_catalog_editor=editor,
        is_deprecated=deprecated,
        pinned_version_id=str(pinned_version_id) if pinned_version_id else None,
    )


async def build_agent_responses(
    db: AsyncSession, agents: list[Agent]
) -> list[AgentResponse]:
    # Simple loop — for large lists we can batch-load catalog rows later.
    return [await build_agent_response(db, a) for a in agents]


async def create_agent(db: AsyncSession, user: User, data: AgentCreate) -> Agent:
    existing = await db.execute(
        select(Agent).where(Agent.owner_id == user.id, Agent.slug == data.slug)
    )
    if existing.scalar_one_or_none():
        raise ConflictError(f"Agent slug '{data.slug}' already exists")

    agent = Agent(
        name=data.name,
        slug=data.slug,
        description=data.description,
        owner_id=user.id,
        instruction=data.instruction,
        model_config_json=data.model_config_json.model_dump(),
        tools=data.tools,
        mcp_servers=[s.model_dump() for s in data.mcp_servers],
        icon=data.icon,
    )
    db.add(agent)
    await db.flush()
    return agent


async def get_agent(db: AsyncSession, agent_id: uuid.UUID) -> Agent | None:
    result = await db.execute(select(Agent).where(Agent.id == agent_id))
    return result.scalar_one_or_none()


async def get_agent_by_slug(
    db: AsyncSession, owner_id: uuid.UUID, slug: str
) -> Agent | None:
    result = await db.execute(
        select(Agent).where(Agent.owner_id == owner_id, Agent.slug == slug)
    )
    return result.scalar_one_or_none()


async def list_agents_for_user(
    db: AsyncSession,
    user: User,
    *,
    offset: int = 0,
    limit: int = 50,
    q: str | None = None,
) -> tuple[list[Agent], int]:
    from sqlalchemy import or_

    base_query = select(Agent).where(Agent.owner_id == user.id)
    if q:
        pattern = f"%{q}%"
        base_query = base_query.where(
            or_(Agent.name.ilike(pattern), Agent.description.ilike(pattern))
        )

    total = (await db.execute(select(func.count()).select_from(base_query.subquery()))).scalar() or 0

    result = await db.execute(
        base_query.order_by(Agent.created_at.desc()).offset(offset).limit(limit)
    )
    return list(result.scalars().all()), total


async def update_agent(db: AsyncSession, agent: Agent, data: AgentUpdate) -> Agent:
    if data.name is not None:
        agent.name = data.name
    if data.description is not None:
        agent.description = data.description
    if data.instruction is not None:
        agent.instruction = data.instruction
    if data.model_config_json is not None:
        agent.model_config_json = data.model_config_json.model_dump()
    if data.tools is not None:
        agent.tools = data.tools
    if data.mcp_servers is not None:
        agent.mcp_servers = [s.model_dump() for s in data.mcp_servers]
    if data.icon is not None:
        agent.icon = data.icon

    await db.flush()
    # `updated_at` is server-updated via onupdate=func.now(); refresh so
    # Pydantic from_attributes doesn't lazy-load it after the session
    # commits on the way out.
    await db.refresh(agent)
    return agent


async def delete_agent(db: AsyncSession, agent: Agent) -> None:
    """Hard-delete the agent and every session (with messages + runtime
    workspace) that belongs to it."""
    from app.services import session_service

    sessions = (await db.execute(
        select(Session).where(Session.agent_id == agent.id)
    )).scalars().all()
    for session in sessions:
        await session_service.delete_session(db, session)

    await db.delete(agent)
    await db.flush()
