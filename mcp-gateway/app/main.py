"""MCP Gateway FastAPI app — JSON-RPC MCP endpoint at /mcp/v1/{agent_id}."""

import json
import logging
from contextlib import asynccontextmanager

from fastapi import FastAPI, Request, Response
from fastapi.responses import JSONResponse
from mcp.types import CallToolRequest, ListToolsRequest

from app.auth import get_current_user, init_oidc
from app.db import async_session_factory
from app.gateway_server import create_gateway_server
from app.platform_servers import register_platform_servers

logger = logging.getLogger(__name__)
gateway_server = create_gateway_server()


@asynccontextmanager
async def lifespan(app: FastAPI):
    await init_oidc()
    try:
        async with async_session_factory() as db:
            await register_platform_servers(db)
    except Exception:
        logger.warning("Platform server registration failed — will retry on next boot", exc_info=True)
    yield


app = FastAPI(title="Aviary MCP Gateway", lifespan=lifespan)


@app.get("/health")
async def health():
    return {"status": "ok"}


@app.api_route("/mcp/v1/{agent_id}", methods=["GET", "POST", "DELETE"])
async def mcp_proxy(agent_id: str, request: Request):
    try:
        claims = await get_current_user(request)
    except ValueError as e:
        return JSONResponse(status_code=401, content={"error": str(e)})

    gateway_server._request_context = {
        "agent_id": agent_id,
        "user_external_id": claims.sub,
    }

    if request.method == "GET":
        return Response(status_code=405, content="SSE not supported in stateless mode")
    if request.method == "DELETE":
        return Response(status_code=200)

    try:
        json_body = json.loads(await request.body())
    except (json.JSONDecodeError, UnicodeDecodeError):
        return JSONResponse(
            status_code=400,
            content={"jsonrpc": "2.0", "error": {"code": -32700, "message": "Parse error"}},
        )

    try:
        return JSONResponse(content=await _dispatch(json_body))
    except Exception:
        logger.exception("MCP request dispatch failed")
        return JSONResponse(
            status_code=500,
            content={
                "jsonrpc": "2.0",
                "error": {"code": -32603, "message": "Internal error"},
                "id": json_body.get("id"),
            },
        )


async def _dispatch(body: dict) -> dict:
    method = body.get("method", "")
    params = body.get("params", {})
    req_id = body.get("id")

    if method == "initialize":
        return {
            "jsonrpc": "2.0",
            "id": req_id,
            "result": {
                "protocolVersion": "2025-03-26",
                "capabilities": {"tools": {"listChanged": True}},
                "serverInfo": {"name": "aviary-mcp-gateway", "version": "0.1.0"},
            },
        }

    if method == "notifications/initialized":
        return {"jsonrpc": "2.0", "id": req_id, "result": {}}

    if method == "tools/list":
        handler = gateway_server.request_handlers.get(ListToolsRequest)
        if not handler:
            return {"jsonrpc": "2.0", "id": req_id, "result": {"tools": []}}
        result = await handler(ListToolsRequest(method="tools/list", params=params))
        tools = result.root.tools if hasattr(result, "root") else []
        return {
            "jsonrpc": "2.0",
            "id": req_id,
            "result": {
                "tools": [
                    {
                        "name": t.name,
                        "description": t.description or "",
                        "inputSchema": t.inputSchema or {},
                    }
                    for t in tools
                ],
            },
        }

    if method == "tools/call":
        handler = gateway_server.request_handlers.get(CallToolRequest)
        if not handler:
            return {
                "jsonrpc": "2.0",
                "id": req_id,
                "error": {"code": -32601, "message": "tools/call not registered"},
            }
        tool_name = params.get("name", "")
        arguments = params.get("arguments", {})
        result = await handler(CallToolRequest(
            method="tools/call",
            params={"name": tool_name, "arguments": arguments},
        ))
        call_result = result.root if hasattr(result, "root") else result
        contents = getattr(call_result, "content", []) or []
        is_error = getattr(call_result, "isError", False) or any(
            hasattr(c, "text") and c.text.startswith("Error:") for c in contents
        )
        return {
            "jsonrpc": "2.0",
            "id": req_id,
            "result": {
                "content": [
                    {"type": "text", "text": c.text} if hasattr(c, "text")
                    else {"type": "text", "text": str(c)}
                    for c in contents
                ],
                "isError": is_error,
            },
        }

    return {
        "jsonrpc": "2.0",
        "id": req_id,
        "error": {"code": -32601, "message": f"Method not found: {method}"},
    }
