"""MCP Gateway core — tools/list and tools/call.

Tools are qualified as `{server}__{tool}`. Only platform-provided servers
are exposed for now; user-registered servers remain in the catalog but are
denied at the edge until RBAC/per-agent binding is reintroduced.

Secret-injected args are stripped from `tools/list` schemas and filled from
Vault on `tools/call` before proxying to the backend.
"""

import logging

from mcp.server import Server
from mcp.types import CallToolResult, TextContent, Tool
from sqlalchemy import select

from aviary_shared.db.models import McpServer, McpTool

from app.connection_pool import pool
from app.db import async_session_factory
from app.secret_injection import get_injected_args, strip_injected_from_schema
from app.vault_client import get_credential

logger = logging.getLogger(__name__)

TOOL_NAME_SEPARATOR = "__"


def create_gateway_server() -> Server:
    server = Server("aviary-mcp-gateway")

    @server.list_tools()
    async def list_tools() -> list[Tool]:
        ctx = getattr(server, "_request_context", {})
        if not ctx.get("user_external_id"):
            return []

        tools: list[Tool] = []
        async with async_session_factory() as db:
            result = await db.execute(
                select(McpServer).where(
                    McpServer.is_platform_provided.is_(True),
                    McpServer.status == "active",
                ),
            )
            servers = result.scalars().all()
            for srv in servers:
                tool_rows = (
                    await db.execute(select(McpTool).where(McpTool.server_id == srv.id))
                ).scalars().all()
                for t in tool_rows:
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
        user_external_id = ctx.get("user_external_id")
        if not user_external_id:
            return [TextContent(type="text", text="Error: missing authentication context")]

        if TOOL_NAME_SEPARATOR not in name:
            return [TextContent(type="text", text=f"Error: invalid tool name format: {name}")]

        server_name, tool_name = name.split(TOOL_NAME_SEPARATOR, 1)

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
