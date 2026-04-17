"""MCP server list + detail pages (read from LiteLLM)."""

from fastapi import APIRouter, Request
from fastapi.responses import HTMLResponse

from aviary_shared.litellm_client import (
    LitellmMCPError,
    get_server,
    list_servers,
    list_tools,
)
from app.routers.pages._templates import templates

router = APIRouter()


def _strip_prefix(prefixed: str, server_name: str) -> str:
    prefix = f"{server_name}__"
    return prefixed[len(prefix):] if prefixed.startswith(prefix) else prefixed


@router.get("/mcp", response_class=HTMLResponse)
async def mcp_server_list(request: Request):
    try:
        servers = await list_servers()
    except LitellmMCPError as exc:
        return HTMLResponse(f"<h1>LiteLLM unreachable: {exc}</h1>", status_code=502)

    server_data = []
    for srv in servers:
        server_id = srv["server_id"]
        tools = await list_tools(server_id=server_id)
        server_data.append({
            "id": server_id,
            "name": srv.get("server_name") or srv.get("alias") or "",
            "description": srv.get("description"),
            "url": srv.get("url"),
            "transport": srv.get("transport") or "http",
            "allow_all_keys": bool(srv.get("allow_all_keys")),
            "tool_count": len(tools),
        })
    return templates.TemplateResponse(
        request, "mcp_servers.html", {"servers": server_data}
    )


@router.get("/mcp/{server_id}", response_class=HTMLResponse)
async def mcp_server_detail(request: Request, server_id: str):
    try:
        srv = await get_server(server_id)
        tools = await list_tools(server_id=server_id)
    except LitellmMCPError as exc:
        return HTMLResponse(f"<h1>Server not found: {exc}</h1>", status_code=404)

    server_name = srv.get("server_name") or srv.get("alias") or ""
    tool_rows = [
        {
            "name": _strip_prefix(t["name"], server_name),
            "qualified_name": t["name"],
            "description": t.get("description"),
        }
        for t in tools
    ]

    return templates.TemplateResponse(request, "mcp_server_detail.html", {
        "server": {
            "id": server_id,
            "name": server_name,
            "description": srv.get("description"),
            "url": srv.get("url"),
            "transport": srv.get("transport") or "http",
            "allow_all_keys": bool(srv.get("allow_all_keys")),
            "tool_count": len(tools),
        },
        "tools": tool_rows,
    })
