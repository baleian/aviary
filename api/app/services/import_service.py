"""Catalog import / fork / restore.

Consumer import creates a new ``agents`` row with the consumer's ``owner_id``
and links it to the source ``CatalogAgent`` via both ``linked_catalog_agent_id``
and ``catalog_import_id``. NOT NULL agents columns are seeded with values
from the resolved snapshot so DB constraints are satisfied — the display
layer always re-reads from catalog, so local values are placeholders.
"""

from __future__ import annotations

import uuid
from typing import Any

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.db.models import (
    Agent,
    AgentVersion,
    CatalogAgent,
    CatalogImport,
    McpAgentToolBinding,
    User,
)
from app.errors import ConflictError, NotFoundError
from app.services.slug_service import resolve_available_slug


def _seed_from_version(version: AgentVersion) -> dict[str, Any]:
    return {
        "name": version.name,
        "description": version.description,
        "icon": version.icon,
        "instruction": version.instruction,
        "model_config_json": dict(version.model_config_json or {}),
        "tools": list(version.tools or []),
        "mcp_servers": list(version.mcp_servers or []),
    }


async def _existing_import_for_user(
    db: AsyncSession, user: User, catalog_agent_id: uuid.UUID
) -> Agent | None:
    row = (await db.execute(
        select(Agent)
        .join(CatalogImport, CatalogImport.id == Agent.catalog_import_id)
        .where(
            Agent.owner_id == user.id,
            CatalogImport.catalog_agent_id == catalog_agent_id,
        )
    )).scalar_one_or_none()
    return row


async def _resolve_snapshot(
    db: AsyncSession, ca: CatalogAgent, pinned_version_id: uuid.UUID | None,
) -> AgentVersion:
    version_id = pinned_version_id or ca.current_version_id
    if version_id is None:
        raise ConflictError("Catalog agent has no current version")
    version = (await db.execute(
        select(AgentVersion).where(AgentVersion.id == version_id)
    )).scalar_one_or_none()
    if version is None:
        raise NotFoundError("Version not found")
    if pinned_version_id is not None and version.catalog_agent_id != ca.id:
        raise NotFoundError("Version does not belong to that catalog agent")
    return version


async def create_import(
    db: AsyncSession,
    user: User,
    catalog_agent_id: uuid.UUID,
    pinned_version_id: uuid.UUID | None,
) -> tuple[Agent, bool]:
    """Upsert an import for this user/catalog pair.

    Returns (agents_row, created) — created=False when already present
    (updates pin to requested value if different)."""
    ca = (await db.execute(
        select(CatalogAgent).where(CatalogAgent.id == catalog_agent_id)
    )).scalar_one_or_none()
    if ca is None:
        raise NotFoundError("Catalog agent not found")

    if not ca.is_published or ca.unpublished_at is not None:
        # Only owners may work with an unpublished catalog; normal import is blocked.
        if ca.owner_id != user.id:
            raise ConflictError("Catalog agent is not currently published")

    existing = await _existing_import_for_user(db, user, ca.id)
    if existing is not None:
        # Already imported — optionally adjust pin, then return as-is.
        if existing.catalog_import_id is not None:
            ci = (await db.execute(
                select(CatalogImport).where(
                    CatalogImport.id == existing.catalog_import_id
                )
            )).scalar_one()
            if ci.pinned_version_id != pinned_version_id:
                ci.pinned_version_id = pinned_version_id
                await db.flush()
        return existing, False

    # Fresh import — snapshot seeds NOT NULL columns, but runtime + API read
    # from catalog on every access.
    version = await _resolve_snapshot(db, ca, pinned_version_id)

    ci = CatalogImport(
        catalog_agent_id=ca.id, pinned_version_id=pinned_version_id
    )
    db.add(ci)
    await db.flush()

    slug = await resolve_available_slug(db, user.id, ca.slug)
    agent = Agent(
        owner_id=user.id,
        slug=slug,
        linked_catalog_agent_id=ca.id,
        catalog_import_id=ci.id,
        **_seed_from_version(version),
    )
    db.add(agent)
    await db.flush()
    return agent, True


async def patch_import_pin(
    db: AsyncSession, agent: Agent, pinned_version_id: uuid.UUID | None,
) -> Agent:
    """Change latest/pinned mode by toggling ``pinned_version_id``."""
    if agent.catalog_import_id is None:
        raise ConflictError("Not an imported agent")

    ci = (await db.execute(
        select(CatalogImport).where(CatalogImport.id == agent.catalog_import_id)
    )).scalar_one()

    if pinned_version_id is not None:
        version = (await db.execute(
            select(AgentVersion).where(AgentVersion.id == pinned_version_id)
        )).scalar_one_or_none()
        if version is None or version.catalog_agent_id != ci.catalog_agent_id:
            raise NotFoundError("Version not found for this catalog agent")

    ci.pinned_version_id = pinned_version_id
    await db.flush()
    await db.refresh(agent)
    return agent


async def delete_import(db: AsyncSession, agent: Agent) -> None:
    """Delete the imported agents row (and its sessions via existing cascade)
    plus the catalog_imports row."""
    from app.services import agent_service

    import_id = agent.catalog_import_id
    await agent_service.delete_agent(db, agent)

    if import_id is not None:
        ci = (await db.execute(
            select(CatalogImport).where(CatalogImport.id == import_id)
        )).scalar_one_or_none()
        if ci is not None:
            await db.delete(ci)
            await db.flush()


async def fork_import(db: AsyncSession, agent: Agent) -> Agent:
    """In-place detach: rewrite the agents row with the current effective
    snapshot, materialise MCP bindings rows, then null out catalog_import_id
    and delete the catalog_imports row. id / slug / sessions are unchanged.
    """
    if agent.catalog_import_id is None:
        raise ConflictError("Agent is not imported")

    ci = (await db.execute(
        select(CatalogImport).where(CatalogImport.id == agent.catalog_import_id)
    )).scalar_one()

    ca = (await db.execute(
        select(CatalogAgent).where(CatalogAgent.id == ci.catalog_agent_id)
    )).scalar_one()

    effective_id = ci.pinned_version_id or ca.current_version_id
    if effective_id is None:
        raise ConflictError("No effective version to fork from")
    version = (await db.execute(
        select(AgentVersion).where(AgentVersion.id == effective_id)
    )).scalar_one()

    seed = _seed_from_version(version)
    agent.name = seed["name"]
    agent.description = seed["description"]
    agent.icon = seed["icon"]
    agent.instruction = seed["instruction"]
    agent.model_config_json = seed["model_config_json"]
    agent.tools = seed["tools"]
    agent.mcp_servers = seed["mcp_servers"]

    # Materialize MCP bindings from the snapshot.
    for b in list(version.mcp_tool_bindings or []):
        db.add(McpAgentToolBinding(
            agent_id=agent.id,
            server_name=b["server_name"],
            tool_name=b["tool_name"],
        ))

    # Detach.
    ci_id = agent.catalog_import_id
    agent.catalog_import_id = None
    agent.linked_catalog_agent_id = None
    await db.flush()

    ci_row = (await db.execute(
        select(CatalogImport).where(CatalogImport.id == ci_id)
    )).scalar_one_or_none()
    if ci_row is not None:
        await db.delete(ci_row)

    await db.flush()
    await db.refresh(agent)
    return agent


# ── Owner "Open as working copy" ──────────────────────────────────────

async def restore_working_copy(
    db: AsyncSession, user: User, ca: CatalogAgent
) -> Agent:
    """Create a new Private (publisher-working-copy) agents row from this
    catalog agent's current snapshot, linked via linked_catalog_agent_id.
    Visibility is NOT changed — an unpublished catalog stays unpublished.
    """
    if ca.current_version_id is None:
        raise ConflictError("Catalog agent has no current version to restore from")
    version = (await db.execute(
        select(AgentVersion).where(AgentVersion.id == ca.current_version_id)
    )).scalar_one()

    slug = await resolve_available_slug(db, user.id, ca.slug)
    agent = Agent(
        owner_id=user.id,
        slug=slug,
        linked_catalog_agent_id=ca.id,
        catalog_import_id=None,
        **_seed_from_version(version),
    )
    db.add(agent)
    await db.flush()

    # Materialize MCP bindings so the working copy has real binding rows.
    for b in list(version.mcp_tool_bindings or []):
        db.add(McpAgentToolBinding(
            agent_id=agent.id,
            server_name=b["server_name"],
            tool_name=b["tool_name"],
        ))
    await db.flush()
    await db.refresh(agent)
    return agent
