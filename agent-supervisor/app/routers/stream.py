"""Streaming API.

Callers (API server, future Temporal workers) invoke `/v1/stream` synchronously
per message — the call blocks until the runtime stream closes. Events land in
Redis (`session:{id}:messages` pub/sub + `session:{id}:stream:chunks` list)
during the call.

Abort: caller supplies `stream_id` (UUID) and `session_id`. Supervisor looks
up the runtime pod address in Redis and POSTs to that pod directly, bypassing
the pool Service load-balancer. Any supervisor replica can handle the abort.
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
    stream_id: str
    # Everything else the runtime's /message expects (content_parts, agent_config,
    # model_config_data, output_format) is forwarded as-is.
    body: dict


class PassthroughRequest(BaseModel):
    pool_name: str
    session_id: str
    agent_id: str
    body: dict


class AbortRequest(BaseModel):
    session_id: str


@router.post("/stream")
async def stream(req: StreamRequest) -> dict:
    return await runtime_proxy.stream_from_pool(
        pool_name=req.pool_name,
        session_id=req.session_id,
        agent_id=req.agent_id,
        stream_id=req.stream_id,
        body=req.body,
    )


@router.post("/stream-passthrough")
async def stream_passthrough(req: PassthroughRequest) -> StreamingResponse:
    """Raw SSE pass-through — no Redis publish. Used by A2A."""
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


@router.post("/streams/{stream_id}/abort")
async def abort(stream_id: str, req: AbortRequest) -> dict:
    return await runtime_proxy.abort_stream(stream_id=stream_id, session_id=req.session_id)


@router.delete("/sessions/{session_id}")
async def cleanup(session_id: str, pool_name: str, agent_id: str) -> dict:
    return await runtime_proxy.cleanup_session(
        pool_name=pool_name, session_id=session_id, agent_id=agent_id,
    )
