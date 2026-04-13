"""Platform-provided MCP server registration + tool discovery on startup."""

from __future__ import annotations

import logging
from datetime import datetime, timezone

import yaml
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from aviary_shared.db.models import McpServer, McpTool

from app.config import settings
from app.connection_pool import pool
from app.secret_injection import annotate_schema_with_injections, get_injected_args

logger = logging.getLogger(__name__)


def _load_specs() -> list[dict]:
    try:
        with open(settings.platform_servers_config) as f:
            data = yaml.safe_load(f)
    except FileNotFoundError:
        logger.info("No platform-servers config at %s", settings.platform_servers_config)
        return []
    if not isinstance(data, list):
        raise ValueError(
            f"Expected list in {settings.platform_servers_config}, got {type(data).__name__}",
        )
    return data


async def _upsert_tools(db: AsyncSession, server: McpServer, raw_tools: list[dict]) -> int:
    result = await db.execute(select(McpTool).where(McpTool.server_id == server.id))
    existing = {t.name: t for t in result.scalars().all()}
    discovered: set[str] = set()

    for raw in raw_tools:
        name = raw["name"]
        discovered.add(name)
        schema = raw.get("inputSchema") or {}
        injected = get_injected_args(server.name, name)
        if injected and isinstance(schema, dict):
            annotate_schema_with_injections(schema, injected)
        if name in existing:
            existing[name].description = raw.get("description", "")
            existing[name].input_schema = schema
        else:
            db.add(McpTool(
                server_id=server.id,
                name=name,
                description=raw.get("description", ""),
                input_schema=schema,
            ))

    for name in set(existing) - discovered:
        await db.delete(existing[name])

    server.last_discovered_at = datetime.now(timezone.utc)
    server.status = "active"
    await db.flush()
    return len(raw_tools)


async def register_platform_servers(db: AsyncSession) -> None:
    for spec in _load_specs():
        result = await db.execute(select(McpServer).where(McpServer.name == spec["name"]))
        server = result.scalar_one_or_none()
        if server is None:
            server = McpServer(
                name=spec["name"],
                description=spec.get("description"),
                transport_type=spec["transport_type"],
                connection_config=spec["connection_config"],
                tags=spec.get("tags", []),
                is_platform_provided=True,
                status="active",
            )
            db.add(server)
            await db.flush()
            logger.info("Registered platform MCP server: %s", spec["name"])
        else:
            server.description = spec.get("description")
            server.connection_config = spec["connection_config"]
            server.tags = spec.get("tags", [])
            server.is_platform_provided = True
            await db.flush()

        try:
            raw_tools = await pool.list_tools(server)
            count = await _upsert_tools(db, server, raw_tools)
            logger.info("Discovered %d tools for %s", count, spec["name"])
        except Exception:
            logger.exception("Tool discovery failed for %s", spec["name"])
            server.status = "degraded"

    await db.commit()
