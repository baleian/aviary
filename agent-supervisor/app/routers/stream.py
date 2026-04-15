"""Streaming API.

Callers (API server, future Temporal workers) invoke `/v1/stream` synchronously
per message — the call blocks until the runtime stream closes. Events land in
Redis (`session:{id}:messages` pub/sub + `session:{id}:stream:chunks` list)
during the call.
"""

from __future__ import annotations

from fastapi import APIRouter
from fastapi.responses import StreamingResponse
from pydantic import BaseModel

from app.services import runtime_proxy

router = APIRouter()


class StreamRequest(BaseModel):
    pool_name: str
    session_id: str
    agent_id: str
    # Everything else the runtime's /message expects (content_parts, agent_config,
    # model_config_data, output_format) is forwarded as-is. Passing through an
    # open dict keeps this router decoupled from runtime body schema evolution.
    body: dict


class AbortRequest(BaseModel):
    pool_name: str


class CleanupRequest(BaseModel):
    pool_name: str
    agent_id: str


@router.post("/stream")
async def stream(req: StreamRequest) -> dict:
    return await runtime_proxy.stream_from_pool(
        pool_name=req.pool_name,
        session_id=req.session_id,
        agent_id=req.agent_id,
        body=req.body,
    )


@router.post("/stream-passthrough")
async def stream_passthrough(req: StreamRequest) -> StreamingResponse:
    """Raw SSE pass-through to the pool — no Redis publish.

    Used by A2A where the calling API router handles selective republishing
    (tagging events with `parent_tool_use_id` and forwarding only tool_use /
    tool_result events to the parent session's Redis channel).
    """
    return StreamingResponse(
        runtime_proxy.passthrough_from_pool(
            pool_name=req.pool_name,
            session_id=req.session_id,
            agent_id=req.agent_id,
            body=req.body,
        ),
        media_type="text/event-stream",
        headers={"Cache-Control": "no-cache", "X-Accel-Buffering": "no"},
    )


@router.post("/sessions/{session_id}/abort")
async def abort(session_id: str, req: AbortRequest) -> dict:
    return await runtime_proxy.abort_session(pool_name=req.pool_name, session_id=session_id)


@router.delete("/sessions/{session_id}")
async def cleanup(session_id: str, pool_name: str, agent_id: str) -> dict:
    # Query params rather than body because DELETE bodies are awkward across clients.
    return await runtime_proxy.cleanup_session(
        pool_name=pool_name, session_id=session_id, agent_id=agent_id,
    )
