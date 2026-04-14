"""MCP tool catalog + per-agent tool bindings.

ACL is intentionally absent until the RBAC redesign. For now all platform
servers are visible to every authenticated user; non-platform servers are
filtered out at the list step (matching the gateway's edge deny policy).

Per-agent bindings are stored inline on `agents.mcp_tool_ids` — a dedicated
join table (McpAgentToolBinding) will replace it when RBAC + ownership
granularity lands.
"""

import uuid

from fastapi import APIRouter, Depends, HTTPException, Query, status
from sqlalchemy import func, or_, select
from sqlalchemy.ext.asyncio import AsyncSession
from sqlalchemy.orm import joinedload

from app.auth.dependencies import get_current_user
from app.deps import get_db
from app.schemas.mcp import (
    McpServerResponse,
    McpToolBindRequest,
    McpToolBindingResponse,
    McpToolResponse,
)
from app.services import agents as agents_svc
from aviary_shared.db.models import Agent, McpServer, McpTool, User

router = APIRouter()

TOOL_NAME_SEPARATOR = "__"


def _tool_to_response(t: McpTool, server_name: str) -> McpToolResponse:
    return McpToolResponse(
        id=str(t.id),
        server_id=str(t.server_id),
        server_name=server_name,
        name=t.name,
        description=t.description,
        input_schema=t.input_schema or {},
        qualified_name=f"{server_name}{TOOL_NAME_SEPARATOR}{t.name}",
    )


@router.get("/servers", response_model=list[McpServerResponse])
async def list_servers(
    _user: User = Depends(get_current_user),
    db: AsyncSession = Depends(get_db),
):
    result = await db.execute(
        select(McpServer)
        .where(McpServer.is_platform_provided.is_(True), McpServer.status == "active")
        .order_by(McpServer.name)
    )
    servers = result.scalars().all()

    out: list[McpServerResponse] = []
    for srv in servers:
        count = (
            await db.execute(
                select(func.count()).select_from(McpTool).where(McpTool.server_id == srv.id)
            )
        ).scalar() or 0
        out.append(McpServerResponse(
            id=str(srv.id),
            name=srv.name,
            description=srv.description,
            tags=srv.tags or [],
            tool_count=count,
        ))
    return out


@router.get("/servers/{server_id}/tools", response_model=list[McpToolResponse])
async def list_server_tools(
    server_id: uuid.UUID,
    _user: User = Depends(get_current_user),
    db: AsyncSession = Depends(get_db),
):
    srv = (await db.execute(select(McpServer).where(McpServer.id == server_id))).scalar_one_or_none()
    if srv is None or not srv.is_platform_provided or srv.status != "active":
        raise HTTPException(status.HTTP_404_NOT_FOUND, "Server not found")

    tools = (
        await db.execute(
            select(McpTool).where(McpTool.server_id == server_id).order_by(McpTool.name)
        )
    ).scalars().all()
    return [_tool_to_response(t, srv.name) for t in tools]


@router.get("/tools/search", response_model=list[McpToolResponse])
async def search_tools(
    q: str = Query(..., min_length=1),
    _user: User = Depends(get_current_user),
    db: AsyncSession = Depends(get_db),
):
    pattern = f"%{q}%"
    tools = (
        await db.execute(
            select(McpTool)
            .join(McpServer)
            .where(
                McpServer.is_platform_provided.is_(True),
                McpServer.status == "active",
                or_(McpTool.name.ilike(pattern), McpTool.description.ilike(pattern)),
            )
            .options(joinedload(McpTool.server))
            .order_by(McpTool.name)
            .limit(50)
        )
    ).scalars().unique().all()
    return [_tool_to_response(t, t.server.name) for t in tools]


# ── Per-agent bindings ──────────────────────────────────────


async def _load_tools_by_ids(
    db: AsyncSession, tool_ids: list[uuid.UUID],
) -> dict[uuid.UUID, McpTool]:
    if not tool_ids:
        return {}
    rows = (
        await db.execute(
            select(McpTool)
            .where(McpTool.id.in_(tool_ids))
            .options(joinedload(McpTool.server))
        )
    ).scalars().unique().all()
    return {t.id: t for t in rows}


@router.get("/agents/{agent_id}/tools", response_model=list[McpToolBindingResponse])
async def list_agent_tools(
    agent_id: uuid.UUID,
    user: User = Depends(get_current_user),
    db: AsyncSession = Depends(get_db),
):
    agent = await agents_svc.require_owner_viewable(db, agent_id, user)
    ids_raw = agent.mcp_tool_ids or []
    tool_uuids: list[uuid.UUID] = []
    for raw in ids_raw:
        try:
            tool_uuids.append(uuid.UUID(raw))
        except (ValueError, TypeError):
            continue
    tools = await _load_tools_by_ids(db, tool_uuids)
    out: list[McpToolBindingResponse] = []
    for tid in tool_uuids:
        t = tools.get(tid)
        if t is None:
            continue
        out.append(McpToolBindingResponse(
            id=str(tid),
            agent_id=str(agent_id),
            tool=_tool_to_response(t, t.server.name),
        ))
    return out


@router.put("/agents/{agent_id}/tools", response_model=list[McpToolBindingResponse])
async def set_agent_tools(
    agent_id: uuid.UUID,
    body: McpToolBindRequest,
    user: User = Depends(get_current_user),
    db: AsyncSession = Depends(get_db),
):
    agent = await agents_svc.require_owner_active(db, agent_id, user)
    try:
        tool_uuids = [uuid.UUID(tid) for tid in body.tool_ids]
    except (ValueError, TypeError) as e:
        raise HTTPException(status.HTTP_400_BAD_REQUEST, f"Invalid tool id: {e}") from e

    tools = await _load_tools_by_ids(db, tool_uuids)
    for tid in tool_uuids:
        if tid not in tools:
            raise HTTPException(status.HTTP_400_BAD_REQUEST, f"Tool not found: {tid}")

    agent.mcp_tool_ids = [str(tid) for tid in tool_uuids]
    await db.flush()
    await db.commit()

    return [
        McpToolBindingResponse(
            id=str(tid),
            agent_id=str(agent_id),
            tool=_tool_to_response(tools[tid], tools[tid].server.name),
        )
        for tid in tool_uuids
    ]
