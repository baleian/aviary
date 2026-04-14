"""MCP server admin — CRUD + tool discovery. is_platform_provided toggle.

Non-platform servers exist in the catalog but are denied at the gateway edge
until RBAC lands; admin can register them now so the ACL/binding work has
fixtures to grant against.
"""

from __future__ import annotations

import uuid
from datetime import datetime, timezone

from fastapi import APIRouter, Form, HTTPException, Request
from fastapi.responses import HTMLResponse, RedirectResponse
from mcp import ClientSession
from mcp.client.streamable_http import streamablehttp_client
from sqlalchemy import func, select

from aviary_shared.db.models import McpServer, McpTool

from app.db import async_session_factory
from app.templates import templates

router = APIRouter()


@router.get("/mcp", response_class=HTMLResponse)
async def list_servers(request: Request):
    async with async_session_factory() as db:
        servers = (
            await db.execute(select(McpServer).order_by(McpServer.name))
        ).scalars().all()
        rows = []
        for srv in servers:
            count = (
                await db.execute(
                    select(func.count()).select_from(McpTool).where(McpTool.server_id == srv.id)
                )
            ).scalar() or 0
            rows.append({"server": srv, "tool_count": count})
    return templates.TemplateResponse(request, "mcp/list.html", {"rows": rows})


@router.get("/mcp/new", response_class=HTMLResponse)
async def new_server_form(request: Request):
    return templates.TemplateResponse(request, "mcp/new.html", {})


@router.post("/mcp")
async def create_server(
    name: str = Form(...),
    description: str = Form(""),
    url: str = Form(...),
    transport_type: str = Form("streamable_http"),
    tags: str = Form(""),
    is_platform_provided: str | None = Form(None),
):
    tag_list = [t.strip() for t in tags.split(",") if t.strip()]
    async with async_session_factory() as db:
        srv = McpServer(
            name=name,
            description=description or None,
            transport_type=transport_type,
            connection_config={"url": url},
            tags=tag_list,
            is_platform_provided=bool(is_platform_provided),
        )
        db.add(srv)
        await db.commit()
        await db.refresh(srv)
    return RedirectResponse(f"/mcp/{srv.id}", status_code=303)


@router.get("/mcp/{server_id}", response_class=HTMLResponse)
async def server_detail(request: Request, server_id: uuid.UUID):
    async with async_session_factory() as db:
        srv = (
            await db.execute(select(McpServer).where(McpServer.id == server_id))
        ).scalar_one_or_none()
        if srv is None:
            raise HTTPException(404, "Server not found")
        tools = (
            await db.execute(
                select(McpTool).where(McpTool.server_id == server_id).order_by(McpTool.name)
            )
        ).scalars().all()
    return templates.TemplateResponse(request, "mcp/detail.html", {
        "server": srv,
        "tools": tools,
        "tags_csv": ", ".join(srv.tags or []),
        "url": (srv.connection_config or {}).get("url", ""),
    })


@router.post("/mcp/{server_id}")
async def update_server(
    server_id: uuid.UUID,
    name: str = Form(...),
    description: str = Form(""),
    url: str = Form(...),
    transport_type: str = Form("streamable_http"),
    tags: str = Form(""),
    is_platform_provided: str | None = Form(None),
):
    async with async_session_factory() as db:
        srv = (
            await db.execute(select(McpServer).where(McpServer.id == server_id))
        ).scalar_one_or_none()
        if srv is None:
            raise HTTPException(404, "Server not found")
        srv.name = name
        srv.description = description or None
        srv.transport_type = transport_type
        srv.connection_config = {"url": url}
        srv.tags = [t.strip() for t in tags.split(",") if t.strip()]
        srv.is_platform_provided = bool(is_platform_provided)
        await db.commit()
    return RedirectResponse(f"/mcp/{server_id}", status_code=303)


@router.post("/mcp/{server_id}/delete")
async def delete_server(server_id: uuid.UUID):
    async with async_session_factory() as db:
        srv = (
            await db.execute(select(McpServer).where(McpServer.id == server_id))
        ).scalar_one_or_none()
        if srv is not None:
            await db.delete(srv)
            await db.commit()
    return RedirectResponse("/mcp", status_code=303)


@router.post("/mcp/{server_id}/discover")
async def discover_tools(server_id: uuid.UUID):
    async with async_session_factory() as db:
        srv = (
            await db.execute(select(McpServer).where(McpServer.id == server_id))
        ).scalar_one_or_none()
        if srv is None:
            raise HTTPException(404, "Server not found")
        url = (srv.connection_config or {}).get("url")
        if not url:
            raise HTTPException(400, "Server has no connection URL")

        try:
            async with streamablehttp_client(url=url) as (read, write, _):
                async with ClientSession(read, write) as session:
                    await session.initialize()
                    result = await session.list_tools()
            raw = [
                {
                    "name": t.name,
                    "description": t.description or "",
                    "inputSchema": getattr(t, "inputSchema", {}) or {},
                }
                for t in result.tools
            ]
        except Exception as e:
            srv.status = "degraded"
            await db.commit()
            raise HTTPException(502, f"Discovery failed: {e}") from e

        existing = {
            t.name: t for t in (
                await db.execute(select(McpTool).where(McpTool.server_id == srv.id))
            ).scalars().all()
        }
        discovered: set[str] = set()
        for raw_tool in raw:
            discovered.add(raw_tool["name"])
            schema = raw_tool.get("inputSchema") or {}
            if raw_tool["name"] in existing:
                existing[raw_tool["name"]].description = raw_tool.get("description", "")
                existing[raw_tool["name"]].input_schema = schema
            else:
                db.add(McpTool(
                    server_id=srv.id,
                    name=raw_tool["name"],
                    description=raw_tool.get("description", ""),
                    input_schema=schema,
                ))
        for name, tool in existing.items():
            if name not in discovered:
                await db.delete(tool)

        srv.last_discovered_at = datetime.now(timezone.utc)
        srv.status = "active"
        await db.commit()
    return RedirectResponse(f"/mcp/{server_id}", status_code=303)
