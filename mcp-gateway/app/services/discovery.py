"""Tool discovery service — connects to backend MCP servers and populates mcp_tools table."""

import logging
import uuid
from datetime import datetime, timezone

from sqlalchemy import delete, select
from sqlalchemy.ext.asyncio import AsyncSession

from aviary_shared.db.models import McpServer, McpTool
from app.mcp.connection_pool import pool

logger = logging.getLogger(__name__)


async def discover_tools(db: AsyncSession, server_id: uuid.UUID) -> int:
    """Connect to a backend MCP server, call tools/list, and upsert discovered tools.

    Returns the number of tools discovered.
    """
    result = await db.execute(select(McpServer).where(McpServer.id == server_id))
    server = result.scalar_one_or_none()
    if server is None:
        raise ValueError(f"Server not found: {server_id}")

    try:
        raw_tools = await pool.list_tools(server)
    except Exception as e:
        server.status = "error"
        await db.flush()
        raise RuntimeError(f"Failed to discover tools from {server.name}: {e}") from e

    # Get existing tools for this server
    result = await db.execute(
        select(McpTool).where(McpTool.server_id == server_id)
    )
    existing = {t.name: t for t in result.scalars().all()}

    discovered_names = set()
    for raw in raw_tools:
        name = raw["name"]
        discovered_names.add(name)

        if name in existing:
            # Update existing tool
            tool = existing[name]
            tool.description = raw.get("description", "")
            tool.input_schema = raw.get("inputSchema", {})
        else:
            # Create new tool
            db.add(McpTool(
                server_id=server_id,
                name=name,
                description=raw.get("description", ""),
                input_schema=raw.get("inputSchema", {}),
            ))

    # Remove tools that no longer exist on the server
    removed = set(existing.keys()) - discovered_names
    if removed:
        for name in removed:
            await db.delete(existing[name])

    server.last_discovered_at = datetime.now(timezone.utc)
    server.status = "active"
    await db.flush()

    return len(raw_tools)
