"""Publish / rollback / unpublish / drift services for catalog agents."""

from __future__ import annotations

import logging
import uuid
from datetime import datetime, timezone

from sqlalchemy import func, select
from sqlalchemy.ext.asyncio import AsyncSession

from app.db.models import (
    Agent,
    AgentVersion,
    CatalogAgent,
    McpAgentToolBinding,
    User,
)
from app.errors import ConflictError, NotFoundError, UnauthorizedError

logger = logging.getLogger(__name__)


# ── Helpers ──────────────────────────────────────────────────────────────

async def _load_bindings(db: AsyncSession, agent_id: uuid.UUID) -> list[dict]:
    rows = (await db.execute(
        select(McpAgentToolBinding.server_name, McpAgentToolBinding.tool_name)
        .where(McpAgentToolBinding.agent_id == agent_id)
        .order_by(McpAgentToolBinding.server_name, McpAgentToolBinding.tool_name)
    )).all()
    return [{"server_name": s, "tool_name": t} for s, t in rows]


async def _next_version_number(db: AsyncSession, catalog_agent_id: uuid.UUID) -> int:
    current_max = (await db.execute(
        select(func.coalesce(func.max(AgentVersion.version_number), 0))
        .where(AgentVersion.catalog_agent_id == catalog_agent_id)
    )).scalar_one()
    return int(current_max) + 1


def _snapshot_fields(agent: Agent, category: str, bindings: list[dict]) -> dict:
    return {
        "name": agent.name,
        "description": agent.description,
        "icon": agent.icon,
        "instruction": agent.instruction,
        "model_config_json": dict(agent.model_config_json or {}),
        "tools": list(agent.tools or []),
        "mcp_servers": list(agent.mcp_servers or []),
        "mcp_tool_bindings": bindings,
        "category": category,
    }


# ── Publish ──────────────────────────────────────────────────────────────

async def publish_version(
    db: AsyncSession, agent: Agent, user: User, *, category: str, release_notes: str | None
) -> AgentVersion:
    """Snapshot the working copy as a new immutable AgentVersion.

    Creates the CatalogAgent on first publish. Subsequent publishes append a
    new version and advance current_version_id.
    """

    if agent.catalog_import_id is not None:
        raise ConflictError(
            "Imported agents cannot be published. Fork it first to make it editable."
        )

    # Row-level lock on the Agent while we compute the next version_number
    # to serialize concurrent publishes on the same working copy.
    locked = await db.execute(
        select(Agent).where(Agent.id == agent.id).with_for_update()
    )
    locked.scalar_one()

    bindings = await _load_bindings(db, agent.id)

    # Load or create the catalog entity.
    catalog_agent: CatalogAgent | None = None
    if agent.linked_catalog_agent_id is not None:
        catalog_agent = (await db.execute(
            select(CatalogAgent).where(
                CatalogAgent.id == agent.linked_catalog_agent_id
            )
        )).scalar_one_or_none()
        if catalog_agent is None:
            # FK was SET NULL due to catalog delete — start fresh.
            agent.linked_catalog_agent_id = None

    if catalog_agent is None:
        catalog_agent = CatalogAgent(
            owner_id=agent.owner_id,
            slug=agent.slug,
            category=category,
            is_published=True,
        )
        db.add(catalog_agent)
        await db.flush()
        agent.linked_catalog_agent_id = catalog_agent.id

    # Republish always flips visibility back on.
    catalog_agent.is_published = True
    catalog_agent.unpublished_at = None
    catalog_agent.category = category

    next_num = await _next_version_number(db, catalog_agent.id)
    version = AgentVersion(
        catalog_agent_id=catalog_agent.id,
        version_number=next_num,
        published_by=user.id,
        release_notes=release_notes,
        **_snapshot_fields(agent, category, bindings),
    )
    db.add(version)
    await db.flush()

    catalog_agent.current_version_id = version.id
    await db.flush()
    await db.refresh(version)
    await db.refresh(catalog_agent)
    return version


# ── Unpublish (whole catalog agent) ─────────────────────────────────────

async def unpublish_catalog_agent(
    db: AsyncSession, catalog_agent: CatalogAgent
) -> CatalogAgent:
    """Flip visibility off and unlink any working copy. Rows/versions stay.

    Existing imports keep functioning — they resolve pinned/latest exactly as
    before; UI derives the deprecated flag from `unpublished_at`.
    """
    catalog_agent.is_published = False
    catalog_agent.unpublished_at = datetime.now(timezone.utc)

    # Clear the link on whichever working copy currently points at us.
    linked = (await db.execute(
        select(Agent).where(Agent.linked_catalog_agent_id == catalog_agent.id)
    )).scalars().all()
    for a in linked:
        a.linked_catalog_agent_id = None

    await db.flush()
    await db.refresh(catalog_agent)
    return catalog_agent


async def republish_catalog_agent(
    db: AsyncSession, catalog_agent: CatalogAgent
) -> CatalogAgent:
    """Turn visibility back on without changing the current_version."""
    catalog_agent.is_published = True
    catalog_agent.unpublished_at = None
    await db.flush()
    await db.refresh(catalog_agent)
    return catalog_agent


# ── Rollback ─────────────────────────────────────────────────────────────

async def rollback_catalog_agent(
    db: AsyncSession, catalog_agent: CatalogAgent, version_id: uuid.UUID
) -> CatalogAgent:
    version = (await db.execute(
        select(AgentVersion).where(AgentVersion.id == version_id)
    )).scalar_one_or_none()
    if version is None or version.catalog_agent_id != catalog_agent.id:
        raise NotFoundError("Version not found for this catalog agent")
    if version.unpublished_at is not None:
        raise ConflictError("Cannot set an unpublished version as latest")

    catalog_agent.current_version_id = version.id
    catalog_agent.is_published = True
    catalog_agent.unpublished_at = None
    catalog_agent.category = version.category
    await db.flush()
    await db.refresh(catalog_agent)
    return catalog_agent


# ── Version-level unpublish ─────────────────────────────────────────────

async def unpublish_version(db: AsyncSession, version: AgentVersion) -> AgentVersion:
    """Mark a single version unpublished. If it was current, auto-fallback
    to the newest still-live version; if none remain, unpublish the whole
    catalog agent."""
    version.unpublished_at = datetime.now(timezone.utc)
    await db.flush()

    catalog_agent = (await db.execute(
        select(CatalogAgent).where(CatalogAgent.id == version.catalog_agent_id)
    )).scalar_one()

    if catalog_agent.current_version_id == version.id:
        fallback = (await db.execute(
            select(AgentVersion)
            .where(
                AgentVersion.catalog_agent_id == catalog_agent.id,
                AgentVersion.id != version.id,
                AgentVersion.unpublished_at.is_(None),
            )
            .order_by(AgentVersion.version_number.desc())
            .limit(1)
        )).scalar_one_or_none()

        if fallback is not None:
            catalog_agent.current_version_id = fallback.id
        else:
            catalog_agent.current_version_id = None
            catalog_agent.is_published = False
            catalog_agent.unpublished_at = datetime.now(timezone.utc)
            linked = (await db.execute(
                select(Agent).where(Agent.linked_catalog_agent_id == catalog_agent.id)
            )).scalars().all()
            for a in linked:
                a.linked_catalog_agent_id = None

    await db.flush()
    await db.refresh(version)
    return version


# ── Drift ────────────────────────────────────────────────────────────────

DRIFT_FIELDS = [
    "name",
    "description",
    "icon",
    "instruction",
    "model_config_json",
    "tools",
    "mcp_servers",
    "category",
]


def _normalize_bindings(bindings: list[dict]) -> set[tuple[str, str]]:
    return {(b["server_name"], b["tool_name"]) for b in bindings}


async def compute_drift(db: AsyncSession, agent: Agent) -> dict:
    """Returns a DriftResponse-compatible dict."""

    if agent.catalog_import_id is not None:
        raise ConflictError("Drift has no meaning for imported agents")

    if agent.linked_catalog_agent_id is None:
        return {
            "is_dirty": True,
            "latest_version_number": None,
            "latest_version_id": None,
            "changed_fields": ["*"],
        }

    catalog_agent = (await db.execute(
        select(CatalogAgent).where(CatalogAgent.id == agent.linked_catalog_agent_id)
    )).scalar_one_or_none()

    from app.services.catalog_permissions import is_catalog_editor

    if catalog_agent is None or not is_catalog_editor(agent, catalog_agent):
        raise UnauthorizedError("Only the catalog owner can compute drift")

    latest_version_id = catalog_agent.current_version_id
    if latest_version_id is None:
        return {
            "is_dirty": True,
            "latest_version_number": None,
            "latest_version_id": None,
            "changed_fields": ["*"],
        }

    version = (await db.execute(
        select(AgentVersion).where(AgentVersion.id == latest_version_id)
    )).scalar_one()

    changed: list[str] = []
    # category on agents maps to catalog_agent.category — we treat the
    # working copy's "intent" as the catalog's current category.
    if catalog_agent.category != version.category:
        changed.append("category")

    pairs = [
        ("name", agent.name, version.name),
        ("description", agent.description, version.description),
        ("icon", agent.icon, version.icon),
        ("instruction", agent.instruction, version.instruction),
        (
            "model_config_json",
            dict(agent.model_config_json or {}),
            dict(version.model_config_json or {}),
        ),
        ("tools", list(agent.tools or []), list(version.tools or [])),
        ("mcp_servers", list(agent.mcp_servers or []), list(version.mcp_servers or [])),
    ]
    for key, local, snap in pairs:
        if local != snap:
            changed.append(key)

    live = _normalize_bindings(await _load_bindings(db, agent.id))
    snapshotted = _normalize_bindings(list(version.mcp_tool_bindings or []))
    if live != snapshotted:
        changed.append("mcp_tool_bindings")

    return {
        "is_dirty": bool(changed),
        "latest_version_number": version.version_number,
        "latest_version_id": str(version.id),
        "changed_fields": changed,
    }
