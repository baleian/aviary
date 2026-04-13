"""Stateless connection pool for backend MCP servers.

Opens a fresh ClientSession per list/call. No persistent connections cached
— backend state must live on the backend server itself.
"""

import logging
from collections.abc import AsyncGenerator
from contextlib import asynccontextmanager

from mcp import ClientSession
from mcp.client.sse import sse_client
from mcp.client.streamable_http import streamablehttp_client
from mcp.types import CallToolResult

from aviary_shared.db.models import McpServer

logger = logging.getLogger(__name__)


class McpConnectionPool:
    @asynccontextmanager
    async def _connect(self, server: McpServer) -> AsyncGenerator[ClientSession, None]:
        transport = server.transport_type
        config = server.connection_config or {}
        headers = config.get("headers", {})

        if transport == "streamable_http":
            url = config["url"]
            async with streamablehttp_client(url=url, headers=headers) as (read, write, _):
                async with ClientSession(read, write) as session:
                    await session.initialize()
                    yield session
        elif transport == "sse":
            url = config["url"]
            async with sse_client(url=url, headers=headers) as (read, write):
                async with ClientSession(read, write) as session:
                    await session.initialize()
                    yield session
        else:
            raise ValueError(f"Unsupported transport: {transport}")

    async def list_tools(self, server: McpServer) -> list[dict]:
        async with self._connect(server) as session:
            result = await session.list_tools()
            return [
                {
                    "name": t.name,
                    "description": t.description or "",
                    "inputSchema": getattr(t, "inputSchema", {}) or {},
                }
                for t in result.tools
            ]

    async def call_tool(self, server: McpServer, tool_name: str, arguments: dict) -> CallToolResult:
        async with self._connect(server) as session:
            return await session.call_tool(tool_name, arguments)


pool = McpConnectionPool()
