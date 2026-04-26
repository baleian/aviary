"""Parse @slug mentions and build the on-the-wire agent_config for each."""

import re
from collections import defaultdict

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.db.models import Agent, User
from app.services import local_mcp_catalog
from aviary_shared.db.models.mcp import McpAgentToolBinding

_MENTION_RE = re.compile(r"@([a-z0-9][a-z0-9-]*[a-z0-9])")

_GATEWAY_MCP_PREFIX = "mcp__gateway__"
_TOOL_SEP = "__"


def extract_mentions(text: str) -> list[str]:
    return list(dict.fromkeys(_MENTION_RE.findall(text)))


def build_mcp_config(legacy_mcp_servers: list) -> dict:
    config: dict = {}
    for srv in legacy_mcp_servers:
        config[srv["name"]] = {"command": srv["command"], "args": srv.get("args", [])}
    return config


def _classify_tool_name(server_name: str, tool_name: str) -> str:
    if local_mcp_catalog.is_local(server_name):
        return f"mcp__{server_name}{_TOOL_SEP}{tool_name}"
    return f"{_GATEWAY_MCP_PREFIX}{server_name}{_TOOL_SEP}{tool_name}"


async def _bound_rows(db: AsyncSession, agent_id) -> list[tuple[str, str]]:
    rows = (
        await db.execute(
            select(McpAgentToolBinding.server_name, McpAgentToolBinding.tool_name)
            .where(McpAgentToolBinding.agent_id == agent_id)
            .order_by(McpAgentToolBinding.server_name, McpAgentToolBinding.tool_name)
        )
    ).all()
    return [(s, t) for s, t in rows]


async def agent_spec(agent, db: AsyncSession) -> dict:
    return _build_spec(agent, await _bound_rows(db, agent.id))


async def resolve_mentioned_agents(
    db: AsyncSession,
    user: User,
    slugs: list[str],
    exclude_agent_id: str | None = None,
) -> list[dict]:
    if not slugs:
        return []

    agents = (await db.execute(
        select(Agent).where(
            Agent.slug.in_(slugs),
            Agent.owner_id == user.id,
        )
    )).scalars().all()

    filtered = [
        a for a in agents
        if not (exclude_agent_id and str(a.id) == exclude_agent_id)
    ]
    if not filtered:
        return []

    bindings: dict[str, list[tuple[str, str]]] = defaultdict(list)
    rows = (await db.execute(
        select(
            McpAgentToolBinding.agent_id,
            McpAgentToolBinding.server_name,
            McpAgentToolBinding.tool_name,
        )
        .where(McpAgentToolBinding.agent_id.in_([a.id for a in filtered]))
        .order_by(McpAgentToolBinding.server_name, McpAgentToolBinding.tool_name)
    )).all()
    for agent_id, server_name, tool_name in rows:
        bindings[agent_id].append((server_name, tool_name))

    by_slug = {a.slug: a for a in filtered}
    ordered = [by_slug[s] for s in slugs if s in by_slug]
    return [_build_spec(a, bindings[a.id]) for a in ordered]


def _build_spec(agent, bound_rows: list[tuple[str, str]]) -> dict:
    mcp_tool_names = [_classify_tool_name(s, t) for s, t in bound_rows]
    merged = list(dict.fromkeys(list(agent.tools or []) + mcp_tool_names))

    mcp_servers = build_mcp_config(agent.mcp_servers)
    used_local = {s for s, _ in bound_rows if local_mcp_catalog.is_local(s)}
    for name in used_local:
        cfg = local_mcp_catalog.get_server_config(name)
        if cfg is not None:
            mcp_servers[name] = cfg

    return {
        "agent_id": str(agent.id),
        "slug": agent.slug,
        "name": agent.name,
        "description": agent.description,
        "runtime_endpoint": agent.runtime_endpoint,
        "model_config": agent.model_config_json,
        "instruction": agent.instruction,
        "tools": merged,
        "mcp_servers": mcp_servers,
    }
