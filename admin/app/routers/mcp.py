"""MCP server CRUD — proxies LiteLLM as the single source of truth.

Admin calls through to LiteLLM's ``/v1/mcp/server`` endpoints (Prisma-backed).
Visibility is controlled entirely by LiteLLM's ``allow_all_keys`` flag —
``true`` exposes the server to every user, ``false`` keeps it hidden. When
per-user RBAC arrives it will live in ``aviary_shared/mcp_access.py``, not
here; this router just speaks to LiteLLM.

Public platform servers (jira, confluence) live in
``config/litellm/config.yaml`` with ``allow_all_keys: true`` and appear in
the list alongside DB-registered ones.
"""

import logging

from fastapi import APIRouter, HTTPException
from pydantic import BaseModel

from aviary_shared.litellm_client import (
    LitellmMCPError,
    create_server,
    delete_server,
    get_server,
    list_servers,
    list_tools,
    update_server,
)

logger = logging.getLogger(__name__)

router = APIRouter()


class McpServerCreate(BaseModel):
    name: str  # LiteLLM `server_name` — becomes the tool prefix
    description: str | None = None
    url: str
    transport: str = "http"
    allow_all_keys: bool = False


class McpServerUpdate(BaseModel):
    name: str | None = None
    description: str | None = None
    url: str | None = None
    transport: str | None = None
    allow_all_keys: bool | None = None


class McpServerResponse(BaseModel):
    id: str
    name: str
    description: str | None
    url: str | None
    transport: str
    allow_all_keys: bool
    tool_count: int


class McpToolResponse(BaseModel):
    name: str  # prefixed: `{server}__{tool}`
    server_name: str
    tool_name: str  # unprefixed
    description: str | None
    input_schema: dict


def _strip_prefix(prefixed: str, server_name: str) -> str:
    prefix = f"{server_name}__"
    return prefixed[len(prefix):] if prefixed.startswith(prefix) else prefixed


async def _server_response(srv: dict) -> McpServerResponse:
    server_id = srv["server_id"]
    tools = await list_tools(server_id=server_id)
    return McpServerResponse(
        id=server_id,
        name=srv.get("server_name") or srv.get("alias") or "",
        description=srv.get("description"),
        url=srv.get("url"),
        transport=srv.get("transport") or "http",
        allow_all_keys=bool(srv.get("allow_all_keys")),
        tool_count=len(tools),
    )


@router.get("/servers", response_model=list[McpServerResponse])
async def list_servers_endpoint():
    try:
        servers = await list_servers()
    except LitellmMCPError as exc:
        raise HTTPException(status_code=502, detail=str(exc)) from exc
    return [await _server_response(s) for s in servers]


@router.post("/servers", response_model=McpServerResponse, status_code=201)
async def create_server_endpoint(body: McpServerCreate):
    try:
        srv = await create_server(
            server_name=body.name,
            url=body.url,
            description=body.description,
            transport=body.transport,
            allow_all_keys=body.allow_all_keys,
        )
    except LitellmMCPError as exc:
        raise HTTPException(status_code=400, detail=str(exc)) from exc
    return await _server_response(srv)


@router.put("/servers/{server_id}", response_model=McpServerResponse)
async def update_server_endpoint(server_id: str, body: McpServerUpdate):
    try:
        srv = await update_server(
            server_id,
            server_name=body.name,
            url=body.url,
            description=body.description,
            transport=body.transport,
            allow_all_keys=body.allow_all_keys,
        )
    except LitellmMCPError as exc:
        raise HTTPException(status_code=400, detail=str(exc)) from exc
    return await _server_response(srv)


@router.delete("/servers/{server_id}", status_code=204)
async def delete_server_endpoint(server_id: str):
    try:
        await delete_server(server_id)
    except LitellmMCPError as exc:
        raise HTTPException(status_code=400, detail=str(exc)) from exc
    # Orphaned `mcp_agent_tool_bindings` rows referencing the deleted server
    # surface as stubs in the user's binding list so the UI can nudge for
    # cleanup — intentionally not cascade-deleted here.
    return None


@router.get("/servers/{server_id}/tools", response_model=list[McpToolResponse])
async def list_server_tools_endpoint(server_id: str):
    try:
        srv = await get_server(server_id)
        tools = await list_tools(server_id=server_id)
    except LitellmMCPError as exc:
        raise HTTPException(status_code=404, detail=str(exc)) from exc
    server_name = srv.get("server_name") or srv.get("alias") or ""
    return [
        McpToolResponse(
            name=t["name"],
            server_name=server_name,
            tool_name=_strip_prefix(t["name"], server_name),
            description=t.get("description"),
            input_schema=t.get("inputSchema") or {},
        )
        for t in tools
    ]
