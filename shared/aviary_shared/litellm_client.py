"""LiteLLM REST client for MCP server catalog management.

LiteLLM is the single source of truth for MCP server + tool catalogs; Aviary's
admin + API servers proxy through this thin wrapper rather than talking to
LiteLLM HTTP directly. The master key (``LITELLM_API_KEY``) is used — LiteLLM's
own admin endpoints require ``PROXY_ADMIN`` role, which the master key has by
default.

Server identifiers: LiteLLM assigns stable hex ``server_id`` values (derived
from name + url + transport + auth) and a mutable ``server_name`` alias. We
expose both. Per-agent bindings and per-user access rows in Aviary DB
reference ``server_name`` (for prefixed tool names) and ``server_id``
(for RBAC), respectively.
"""

from __future__ import annotations

import logging
import os
from typing import Any

import httpx

logger = logging.getLogger(__name__)


class LitellmMCPError(Exception):
    """Raised when LiteLLM returns a non-success status on an MCP admin call."""


def _base_url() -> str:
    url = os.environ.get("LITELLM_URL")
    if not url:
        raise RuntimeError("LITELLM_URL env var is required")
    return url.rstrip("/")


def _admin_key() -> str:
    key = os.environ.get("LITELLM_API_KEY")
    if not key:
        raise RuntimeError("LITELLM_API_KEY env var is required")
    return key


def _headers() -> dict[str, str]:
    return {"Authorization": f"Bearer {_admin_key()}"}


async def _request(method: str, path: str, **kwargs: Any) -> httpx.Response:
    async with httpx.AsyncClient(timeout=15) as client:
        resp = await client.request(
            method, f"{_base_url()}{path}", headers=_headers(), **kwargs
        )
        if resp.status_code >= 400:
            detail = resp.text[:500]
            raise LitellmMCPError(f"{method} {path} → {resp.status_code}: {detail}")
        return resp


async def list_servers() -> list[dict]:
    """Return all MCP servers known to LiteLLM (YAML + DB-registered)."""
    resp = await _request("GET", "/v1/mcp/server")
    return resp.json()


async def get_server(server_id: str) -> dict:
    resp = await _request("GET", f"/v1/mcp/server/{server_id}")
    return resp.json()


async def create_server(
    *,
    server_name: str,
    url: str,
    description: str | None = None,
    transport: str = "http",
    allow_all_keys: bool = False,
) -> dict:
    """Register a new MCP server. Private by default (``allow_all_keys=False``)
    so new servers are invisible until an admin grants user-level access via
    ``McpUserServerAccess``."""
    payload = {
        "server_name": server_name,
        "url": url,
        "description": description,
        "transport": transport,
        "allow_all_keys": allow_all_keys,
    }
    resp = await _request("POST", "/v1/mcp/server", json=payload)
    return resp.json()


async def update_server(
    server_id: str,
    *,
    server_name: str | None = None,
    url: str | None = None,
    description: str | None = None,
    transport: str | None = None,
    allow_all_keys: bool | None = None,
) -> dict:
    payload: dict[str, Any] = {"server_id": server_id}
    if server_name is not None:
        payload["server_name"] = server_name
    if url is not None:
        payload["url"] = url
    if description is not None:
        payload["description"] = description
    if transport is not None:
        payload["transport"] = transport
    if allow_all_keys is not None:
        payload["allow_all_keys"] = allow_all_keys
    resp = await _request("PUT", "/v1/mcp/server", json=payload)
    return resp.json()


async def delete_server(server_id: str) -> None:
    await _request("DELETE", f"/v1/mcp/server/{server_id}")


async def list_tools(server_id: str | None = None) -> list[dict]:
    """List aggregated MCP tools (optionally scoped to a single server).

    Tool names are prefixed by LiteLLM as ``{server_name}__{tool_name}``
    (via ``MCP_TOOL_PREFIX_SEPARATOR=__``). LiteLLM's ``/v1/mcp/tools``
    endpoint returns the full aggregated catalog regardless of query params,
    so per-server scoping is done client-side by matching the name prefix.
    """
    resp = await _request("GET", "/v1/mcp/tools")
    data = resp.json()
    tools = data.get("tools") if isinstance(data, dict) else data

    if server_id is None:
        return tools

    srv = await get_server(server_id)
    name = srv.get("server_name") or srv.get("alias") or ""
    if not name:
        return []
    prefix = f"{name}__"
    return [t for t in tools if t.get("name", "").startswith(prefix)]
