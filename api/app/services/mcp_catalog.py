"""MCP catalog fetcher — relays the gateway view, scoped by X-Aviary-User-Sub."""

from __future__ import annotations

from mcp import ClientSession
from mcp.client.streamable_http import streamablehttp_client

from app.auth.oidc import idp_enabled
from app.config import settings


async def fetch_tools(user_token: str, user_sub: str) -> list[dict]:
    if not settings.mcp_gateway_url:
        return []
    base = settings.mcp_gateway_url.rstrip("/")
    # Bearer is only for the gateway's native auth admission. Identity comes from X-Aviary-User-Sub.
    bearer = user_token if idp_enabled() else (settings.mcp_gateway_api_key or "")
    headers = {
        "Authorization": f"Bearer {bearer}",
        "X-Aviary-User-Sub": user_sub,
    }
    async with streamablehttp_client(
        f"{base}/mcp", headers=headers,
    ) as (read_stream, write_stream, _):
        async with ClientSession(read_stream, write_stream) as session:
            await session.initialize()
            result = await session.list_tools()
    return [
        {
            "name": t.name,
            "description": getattr(t, "description", None),
            "inputSchema": getattr(t, "inputSchema", None) or {},
        }
        for t in result.tools
    ]
