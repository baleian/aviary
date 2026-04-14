"""MCP Gateway core — tools/list and tools/call with per-agent binding filter.

Tools are qualified as `{server}__{tool}`. Each agent's request is scoped to
the tools in `agents.mcp_tool_ids`: tools/list returns only those, and
tools/call rejects unbound tools. Non-platform servers remain denied at the
edge until RBAC lands.

Secret-injected args are stripped from `tools/list` schemas and filled from
Vault on `tools/call` before proxying to the backend.
"""

import logging
import uuid

from mcp.server import Server
from mcp.types import CallToolResult, TextContent, Tool
from sqlalchemy import select
from sqlalchemy.orm import joinedload

from aviary_shared.db.models import Agent, McpServer, McpTool

from app.connection_pool import pool
from app.db import async_session_factory
from app.secret_injection import get_injected_args, strip_injected_from_schema
from app.vault_client import get_credential

logger = logging.getLogger(__name__)

TOOL_NAME_SEPARATOR = "__"


def _parse_ids(raw_ids: list[str] | None) -> list[uuid.UUID]:
    out: list[uuid.UUID] = []
    for raw in raw_ids or []:
        try:
            out.append(uuid.UUID(raw))
        except (ValueError, TypeError):
            continue
    return out


def create_gateway_server() -> Server:
    server = Server("aviary-mcp-gateway")

    @server.list_tools()
    async def list_tools() -> list[Tool]:
        ctx = getattr(server, "_request_context", {})
        agent_id = ctx.get("agent_id")
        user_external_id = ctx.get("user_external_id")
        if not agent_id or not user_external_id:
            return []

        try:
            agent_uuid = uuid.UUID(agent_id)
        except (ValueError, TypeError):
            return []

        tools: list[Tool] = []
        async with async_session_factory() as db:
            agent = (
                await db.execute(select(Agent).where(Agent.id == agent_uuid))
            ).scalar_one_or_none()
            if agent is None:
                return []

            tool_uuids = _parse_ids(agent.mcp_tool_ids)
            if not tool_uuids:
                return []

            rows = (
                await db.execute(
                    select(McpTool)
                    .where(McpTool.id.in_(tool_uuids))
                    .options(joinedload(McpTool.server))
                )
            ).scalars().unique().all()

            for t in rows:
                srv = t.server
                if not srv.is_platform_provided or srv.status != "active":
                    continue
                injected = get_injected_args(srv.name, t.name)
                schema = strip_injected_from_schema(t.input_schema or {}, injected)
                tools.append(Tool(
                    name=f"{srv.name}{TOOL_NAME_SEPARATOR}{t.name}",
                    description=t.description or "",
                    inputSchema=schema,
                ))
        return tools

    @server.call_tool()
    async def call_tool(name: str, arguments: dict | None) -> list[TextContent]:
        ctx = getattr(server, "_request_context", {})
        agent_id = ctx.get("agent_id")
        user_external_id = ctx.get("user_external_id")
        if not agent_id or not user_external_id:
            return [TextContent(type="text", text="Error: missing authentication context")]

        if TOOL_NAME_SEPARATOR not in name:
            return [TextContent(type="text", text=f"Error: invalid tool name format: {name}")]

        server_name, tool_name = name.split(TOOL_NAME_SEPARATOR, 1)

        try:
            agent_uuid = uuid.UUID(agent_id)
        except (ValueError, TypeError):
            return [TextContent(type="text", text="Error: invalid agent id")]

        async with async_session_factory() as db:
            mcp_server = (
                await db.execute(select(McpServer).where(McpServer.name == server_name))
            ).scalar_one_or_none()
            if mcp_server is None:
                return [TextContent(type="text", text=f"Error: unknown server: {server_name}")]
            if not mcp_server.is_platform_provided:
                return [TextContent(
                    type="text",
                    text=f"Error: server {server_name} not accessible (non-platform servers deny-by-default)",
                )]
            if mcp_server.status != "active":
                return [TextContent(type="text", text=f"Error: server {server_name} is not active")]

            mcp_tool = (
                await db.execute(select(McpTool).where(
                    McpTool.server_id == mcp_server.id,
                    McpTool.name == tool_name,
                ))
            ).scalar_one_or_none()
            if mcp_tool is None:
                return [TextContent(type="text", text=f"Error: unknown tool: {tool_name}")]

            agent = (
                await db.execute(select(Agent).where(Agent.id == agent_uuid))
            ).scalar_one_or_none()
            if agent is None:
                return [TextContent(type="text", text="Error: agent not found")]

            bound_uuids = set(_parse_ids(agent.mcp_tool_ids))
            if mcp_tool.id not in bound_uuids:
                return [TextContent(type="text", text=f"Error: tool {name} not bound to agent")]

        final_args = dict(arguments or {})
        for arg_name, mapping in get_injected_args(server_name, tool_name).items():
            vault_key = mapping["vault_key"]
            token = await get_credential(user_external_id, vault_key)
            if not token:
                return [TextContent(
                    type="text",
                    text=f"Error: no '{vault_key}' credential configured for your account.",
                )]
            final_args[arg_name] = token

        try:
            call_result: CallToolResult = await pool.call_tool(mcp_server, tool_name, final_args)
        except Exception as e:
            logger.exception("Tool call failed: %s on %s", tool_name, server_name)
            return [TextContent(type="text", text=f"Error: tool call failed: {e}")]

        out: list[TextContent] = []
        for item in call_result.content:
            if hasattr(item, "text"):
                out.append(TextContent(type="text", text=item.text))
            else:
                out.append(TextContent(type="text", text=str(item)))
        return out

    return server
