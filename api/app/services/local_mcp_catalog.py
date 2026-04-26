"""Catalog for config.yaml mcp_servers — tools shaped as ``{server}__{tool}``."""

from __future__ import annotations

import asyncio
import logging
from functools import lru_cache

from mcp import ClientSession, StdioServerParameters
from mcp.client.stdio import stdio_client
from mcp.client.streamable_http import streamablehttp_client

from app.config import settings
from aviary_shared.local_mcp import load_servers

logger = logging.getLogger(__name__)

_PROBE_TIMEOUT_S = 30.0
_TOOL_NAME_SEPARATOR = "__"


@lru_cache(maxsize=1)
def _servers() -> dict[str, dict]:
    return load_servers(settings.llm_backends_config_path)


def is_local(server_name: str) -> bool:
    return server_name in _servers()


def get_server_config(server_name: str) -> dict | None:
    return _servers().get(server_name)


_tool_cache: dict[str, list[dict]] = {}
_tool_cache_lock = asyncio.Lock()


async def probe_tools(server_name: str) -> list[dict]:
    cached = _tool_cache.get(server_name)
    if cached is not None:
        return cached
    async with _tool_cache_lock:
        cached = _tool_cache.get(server_name)
        if cached is not None:
            return cached
        cfg = _servers().get(server_name)
        if not cfg:
            return []
        try:
            tools = await asyncio.wait_for(_probe(cfg), timeout=_PROBE_TIMEOUT_S)
        except Exception as e:  # noqa: BLE001
            logger.warning("MCP probe failed for %s: %s", server_name, e)
            tools = []
        _tool_cache[server_name] = tools
        return tools


async def _probe(cfg: dict) -> list[dict]:
    if cfg.get("type") == "http":
        async with streamablehttp_client(
            cfg["url"], headers=dict(cfg.get("headers") or {}),
        ) as (read, write, _):
            async with ClientSession(read, write) as session:
                await session.initialize()
                result = await session.list_tools()
    else:
        params = StdioServerParameters(
            command=cfg["command"],
            args=list(cfg.get("args") or []),
            env=cfg.get("env"),
        )
        async with stdio_client(params) as (read, write):
            async with ClientSession(read, write) as session:
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


async def fetch_all_tools() -> list[dict]:
    out: list[dict] = []
    for name in _servers():
        for t in await probe_tools(name):
            out.append({
                "name": f"{name}{_TOOL_NAME_SEPARATOR}{t['name']}",
                "description": t.get("description"),
                "inputSchema": t.get("inputSchema") or {},
            })
    return out
