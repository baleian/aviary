"""Resolve an on-the-wire ``agent_config`` for any agents row.

Private / Publisher-working-copy rows use their local fields + MCP binding
rows. Consumer-Imported rows read the effective AgentVersion snapshot
(pinned or latest) from the catalog — the snapshot's mcp_tool_bindings JSON
is the source of truth, not McpAgentToolBinding rows (which don't exist for
imports).
"""

from __future__ import annotations

from typing import Any

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.db.models import Agent, AgentVersion, CatalogAgent, CatalogImport
from app.errors import StateError
from app.services.mention_service import (
    _MCP_PREFIX,
    _MCP_TOOL_SEPARATOR,
    agent_spec,
    build_mcp_config,
)


async def resolve_effective_version(
    db: AsyncSession, agent: Agent
) -> tuple[CatalogAgent, AgentVersion]:
    """For an imported agent, return (catalog_agent, effective AgentVersion)."""
    if agent.catalog_import_id is None:
        raise StateError("Not an imported agent")

    ci = (await db.execute(
        select(CatalogImport).where(CatalogImport.id == agent.catalog_import_id)
    )).scalar_one_or_none()
    if ci is None:
        raise StateError("Catalog import not found")

    ca = (await db.execute(
        select(CatalogAgent).where(CatalogAgent.id == ci.catalog_agent_id)
    )).scalar_one_or_none()
    if ca is None:
        raise StateError("Catalog agent not found")

    effective_version_id = ci.pinned_version_id or ca.current_version_id
    if effective_version_id is None:
        raise StateError(
            "No resolvable version for this import (no pin, no current)"
        )
    version = (await db.execute(
        select(AgentVersion).where(AgentVersion.id == effective_version_id)
    )).scalar_one_or_none()
    if version is None:
        raise StateError("Effective version no longer exists")
    return ca, version


def _version_mcp_tool_names(mcp_tool_bindings: list[dict[str, Any]]) -> list[str]:
    return [
        f"{_MCP_PREFIX}{b['server_name']}{_MCP_TOOL_SEPARATOR}{b['tool_name']}"
        for b in (mcp_tool_bindings or [])
    ]


def _version_as_spec(
    agent: Agent, version: AgentVersion
) -> dict:
    """Build the wire spec from a catalog version snapshot.

    ``agent`` is the consumer's local agents row — its id/slug/runtime_endpoint
    are preserved (slug is user-scoped, runtime_endpoint is local choice). The
    rest comes from the immutable snapshot."""
    mcp_names = _version_mcp_tool_names(list(version.mcp_tool_bindings or []))
    merged_tools = list(dict.fromkeys(list(version.tools or []) + mcp_names))
    return {
        "agent_id": str(agent.id),
        "slug": agent.slug,
        "name": version.name,
        "description": version.description,
        "runtime_endpoint": agent.runtime_endpoint,
        "model_config": dict(version.model_config_json or {}),
        "instruction": version.instruction,
        "tools": merged_tools,
        "mcp_servers": build_mcp_config(list(version.mcp_servers or [])),
    }


async def resolve_agent_config(db: AsyncSession, agent: Agent) -> dict:
    """Entry point — replaces direct ``mention_service.agent_spec`` calls so
    Imported agents resolve via catalog snapshot automatically."""
    if agent.catalog_import_id is None:
        return await agent_spec(agent, db)

    _, version = await resolve_effective_version(db, agent)
    return _version_as_spec(agent, version)
