"""MCP Gateway Server — dynamic tools/list and tools/call with ACL filtering.

Uses the low-level mcp.server.Server API (not FastMCP decorators) to handle
tool requests dynamically based on agent bindings and user ACL.
"""

import logging
import uuid

from mcp.server import Server
from mcp.types import (
    CallToolResult,
    TextContent,
    Tool,
)
from sqlalchemy import select
from sqlalchemy.orm import joinedload

from aviary_shared.db.models import McpAgentToolBinding, McpServer, McpTool
from app.db.session import async_session_factory
from app.mcp.connection_pool import pool
from app.services.acl import check_tool_access

logger = logging.getLogger(__name__)

TOOL_NAME_SEPARATOR = "__"


def create_gateway_server() -> Server:
    """Create and configure the MCP gateway server instance."""
    server = Server("aviary-mcp-gateway")

    @server.list_tools()
    async def list_tools() -> list[Tool]:
        """Return tools bound to the agent, filtered by user ACL.

        The agent_id and user_external_id are injected into the request context
        by the Streamable HTTP handler (see proxy router).
        """
        # These are set by the ASGI middleware/handler before the MCP session starts.
        # For now, we read them from server._request_context which is set per-request.
        ctx = getattr(server, "_request_context", {})
        agent_id = ctx.get("agent_id")
        user_external_id = ctx.get("user_external_id")

        if not agent_id or not user_external_id:
            logger.warning("list_tools called without agent_id or user_external_id")
            return []

        agent_uuid = uuid.UUID(agent_id)
        tools: list[Tool] = []

        async with async_session_factory() as db:
            # Fetch all tool bindings for this agent, with tool and server eagerly loaded
            result = await db.execute(
                select(McpAgentToolBinding)
                .where(McpAgentToolBinding.agent_id == agent_uuid)
                .options(
                    joinedload(McpAgentToolBinding.tool).joinedload(McpTool.server)
                )
            )
            bindings = result.scalars().unique().all()

            for binding in bindings:
                tool = binding.tool
                srv = tool.server

                if srv.status != "active":
                    continue

                # ACL check
                perm = await check_tool_access(
                    db, user_external_id, srv.id, tool.id
                )
                if perm != "use":
                    continue

                qualified_name = f"{srv.name}{TOOL_NAME_SEPARATOR}{tool.name}"
                tools.append(
                    Tool(
                        name=qualified_name,
                        description=tool.description or "",
                        inputSchema=tool.input_schema or {},
                    )
                )

        return tools

    @server.call_tool()
    async def call_tool(name: str, arguments: dict | None) -> list[TextContent]:
        """Route a tool call to the correct backend MCP server."""
        ctx = getattr(server, "_request_context", {})
        agent_id = ctx.get("agent_id")
        user_external_id = ctx.get("user_external_id")

        if not agent_id or not user_external_id:
            return [TextContent(type="text", text="Error: missing authentication context")]

        # Parse composite name: "github__create_issue" → server="github", tool="create_issue"
        if TOOL_NAME_SEPARATOR not in name:
            return [TextContent(type="text", text=f"Error: invalid tool name format: {name}")]

        server_name, tool_name = name.split(TOOL_NAME_SEPARATOR, 1)

        async with async_session_factory() as db:
            # Look up the server and tool
            result = await db.execute(
                select(McpServer).where(McpServer.name == server_name)
            )
            mcp_server = result.scalar_one_or_none()
            if mcp_server is None:
                return [TextContent(type="text", text=f"Error: unknown server: {server_name}")]

            if mcp_server.status != "active":
                return [TextContent(type="text", text=f"Error: server {server_name} is not active")]

            result = await db.execute(
                select(McpTool).where(
                    McpTool.server_id == mcp_server.id,
                    McpTool.name == tool_name,
                )
            )
            mcp_tool = result.scalar_one_or_none()
            if mcp_tool is None:
                return [TextContent(type="text", text=f"Error: unknown tool: {tool_name}")]

            # Verify binding exists
            agent_uuid = uuid.UUID(agent_id)
            result = await db.execute(
                select(McpAgentToolBinding).where(
                    McpAgentToolBinding.agent_id == agent_uuid,
                    McpAgentToolBinding.tool_id == mcp_tool.id,
                )
            )
            if result.scalar_one_or_none() is None:
                return [TextContent(type="text", text=f"Error: tool {name} not bound to agent")]

            # ACL check
            perm = await check_tool_access(
                db, user_external_id, mcp_server.id, mcp_tool.id
            )
            if perm != "use":
                return [TextContent(type="text", text=f"Error: permission denied for tool {name}")]

        # Forward to backend MCP server (user token NOT forwarded)
        try:
            call_result: CallToolResult = await pool.call_tool(
                mcp_server, tool_name, arguments or {}
            )
            # Convert CallToolResult content to TextContent list
            contents = []
            for item in call_result.content:
                if hasattr(item, "text"):
                    contents.append(TextContent(type="text", text=item.text))
                else:
                    contents.append(TextContent(type="text", text=str(item)))
            return contents
        except Exception as e:
            logger.exception("Tool call failed: %s on %s", tool_name, server_name)
            return [TextContent(type="text", text=f"Error: tool call failed: {e}")]

    return server
