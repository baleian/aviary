"""Shared MCP catalog fetcher.

LiteLLM owns all visibility/ACL decisions. We open an MCP session to
``/mcp`` carrying the caller's identity (``X-Aviary-User-Sub``) and
relay whatever LiteLLM's guardrail returns. The Bearer token is needed
only for LiteLLM's native auth admission — JWT in real mode, master
key in dev.
"""

from __future__ import annotations

import os

from mcp import ClientSession
from mcp.client.streamable_http import streamablehttp_client

from app.auth.oidc import idp_enabled


async def fetch_tools(user_token: str, user_sub: str) -> list[dict]:
    """Return every MCP tool the caller is allowed to see.

    Each entry: ``{"name": "<server>__<tool>", "description": str | None,
    "inputSchema": dict}``.
    """
    base = os.environ["LITELLM_URL"].rstrip("/")
    bearer = user_token if idp_enabled() else os.environ["LITELLM_API_KEY"]
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
