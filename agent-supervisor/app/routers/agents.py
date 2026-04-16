"""Session-centric API used by the API server and future orchestrators.

The supervisor is stateless from the DB's point of view but keeps an
in-memory registry of active publish handlers so that aborts cancel the
right stream. Abort propagation uses HTTP connection closure:

    API cancels httpx → supervisor's outbound stream task cancel →
    httpx client closes TCP → runtime pod's req.on("close") fires → SDK abort.

No direct pod-to-pod routing needed; the Service load-balanced TCP
connection is pod-pinned for its lifetime.
"""

from __future__ import annotations

import asyncio
import json
import logging
import time

import httpx
from fastapi import APIRouter, Request
from fastapi.responses import StreamingResponse
from pydantic import BaseModel

from app import assembly, metrics, redis_client
from app.routing import resolve_runtime_base

logger = logging.getLogger(__name__)

router = APIRouter()

# (session_id, agent_id) → running publish task. Used by abort to cancel.
# If the same session+agent publishes again before the previous is cancelled,
# the previous task is cancelled first.
_active: dict[tuple[str, str | None], asyncio.Task] = {}
_DISCONNECT_POLL_SECONDS = 0.5


def _registry_key(session_id: str, agent_id: str | None) -> tuple[str, str | None]:
    return (session_id, agent_id)


async def _watch_disconnect(request: Request) -> None:
    """Return when the client closes its side of the HTTP connection."""
    while True:
        if await request.is_disconnected():
            return
        await asyncio.sleep(_DISCONNECT_POLL_SECONDS)


@router.post("/sessions/{session_id}/message")
async def proxy_session_message(session_id: str, request: Request):
    """Transparent SSE passthrough. Used by workflow / A2A sub-agent paths
    that do in-process event transformation rather than going through Redis."""
    body = await request.json()
    base = resolve_runtime_base(body.get("runtime_endpoint"))

    async def generate():
        try:
            async with httpx.AsyncClient() as client:
                async with client.stream(
                    "POST", f"{base}/message", json=body, timeout=None,
                ) as resp:
                    if resp.status_code != 200:
                        err = await resp.aread()
                        logger.error("Runtime stream %d: %s", resp.status_code, err)
                        yield (
                            f"data: {json.dumps({'type': 'error', 'message': f'Agent runtime error ({resp.status_code})'})}\n\n"
                        ).encode()
                        return
                    async for chunk in resp.aiter_bytes():
                        yield chunk
        except httpx.HTTPError:
            logger.exception("SSE proxy error for session %s", session_id)
            yield (
                f"data: {json.dumps({'type': 'error', 'message': 'Agent runtime connection failed'})}\n\n"
            ).encode()

    return StreamingResponse(
        generate(),
        media_type="text/event-stream",
        headers={"Cache-Control": "no-cache", "X-Accel-Buffering": "no"},
    )


async def _do_publish(
    session_id: str, body: dict,
) -> dict:
    """Stream SSE from runtime, publish each event to Redis, assemble final."""
    base = resolve_runtime_base(body.get("runtime_endpoint"))
    reached_runtime = False
    error_message: str | None = None
    started = time.monotonic()

    try:
        async with httpx.AsyncClient() as client:
            async with client.stream(
                "POST", f"{base}/message", json=body, timeout=None,
            ) as resp:
                if resp.status_code != 200:
                    err = (await resp.aread()).decode(errors="replace")[:500]
                    logger.error("Runtime stream %d: %s", resp.status_code, err)
                    error_message = f"Agent runtime error ({resp.status_code}): {err}"
                else:
                    async for line in resp.aiter_lines():
                        if not line.startswith("data: "):
                            continue
                        event = json.loads(line[6:])
                        etype = event.get("type")
                        metrics.sse_events_total.labels(event_type=etype or "unknown").inc()
                        if etype == "query_started":
                            reached_runtime = True
                            continue
                        if etype == "error":
                            error_message = event.get("message", "Agent runtime error")
                            break
                        await redis_client.append_stream_chunk(session_id, event)
                        await redis_client.publish_message(session_id, event)
    except httpx.HTTPError as e:
        logger.exception("SSE proxy error for session %s", session_id)
        error_message = f"Agent runtime connection failed: {e}"

    metrics.publish_duration_seconds.observe(time.monotonic() - started)

    if error_message:
        metrics.publish_requests_total.labels(status="error").inc()
        await redis_client.set_stream_status(session_id, "error")
        return {"status": "error", "message": error_message, "reached_runtime": reached_runtime}

    chunks = await redis_client.get_stream_chunks(session_id)
    assembled_text, assembled_blocks = assembly.rebuild_blocks_from_chunks(chunks)
    await assembly.merge_a2a_events(session_id, assembled_blocks)

    await redis_client.set_stream_status(session_id, "complete")
    metrics.publish_requests_total.labels(status="complete").inc()
    return {
        "status": "complete",
        "reached_runtime": reached_runtime,
        "assembled_text": assembled_text,
        "assembled_blocks": assembled_blocks,
    }


@router.post("/sessions/{session_id}/publish")
async def publish_session_message(session_id: str, request: Request):
    """Consume runtime SSE → Redis (for WS broadcast + replay buffer) → return
    the assembled final message to the caller.

    Two ways this handler terminates:
      1. Runtime stream completes → normal response.
      2. Abort: either the caller disconnects (race below) or a sibling
         /abort call cancels our publish task. In both cases the outbound
         httpx stream context exits, which closes the TCP connection to the
         specific runtime pod, which triggers its close-event handler and
         aborts the SDK.
    """
    body = await request.json()
    agent_id = (body.get("agent_config") or {}).get("agent_id")
    key = _registry_key(session_id, agent_id)

    # If another publish is in flight for this session/agent, cancel it first.
    prior = _active.get(key)
    if prior and not prior.done():
        logger.warning("Cancelling in-flight publish for %s/%s", session_id, agent_id)
        prior.cancel()

    publish_task = asyncio.create_task(_do_publish(session_id, body))
    disconnect_task = asyncio.create_task(_watch_disconnect(request))
    _active[key] = publish_task

    try:
        done, pending = await asyncio.wait(
            [publish_task, disconnect_task],
            return_when=asyncio.FIRST_COMPLETED,
        )
        for t in pending:
            t.cancel()

        if publish_task in done:
            try:
                return publish_task.result()
            except asyncio.CancelledError:
                await redis_client.set_stream_status(session_id, "error")
                metrics.publish_requests_total.labels(status="aborted").inc()
                return {"status": "aborted", "reached_runtime": False}

        # Caller disconnected: publish_task is now cancelled.
        await redis_client.set_stream_status(session_id, "error")
        metrics.publish_requests_total.labels(status="disconnected").inc()
        return {"status": "disconnected", "reached_runtime": False}
    finally:
        # Best-effort cleanup. Whatever is cancelled should settle before we pop.
        for t in (publish_task, disconnect_task):
            if not t.done():
                t.cancel()
        _active.pop(key, None)


class _AbortBody(BaseModel):
    agent_id: str | None = None


@router.post("/sessions/{session_id}/abort")
async def abort_session(session_id: str, body: _AbortBody):
    """Cancel the in-flight publish task for (session_id, agent_id).

    Cancelling the task closes the supervisor → runtime TCP connection,
    which fires the runtime pod's `req.on('close')` handler and aborts the
    SDK query. No HTTP forward to the runtime required.
    """
    key = _registry_key(session_id, body.agent_id)
    task = _active.get(key)
    if not task or task.done():
        return {"ok": False, "reason": "not_found"}
    task.cancel()
    return {"ok": True}


class _CleanupBody(BaseModel):
    runtime_endpoint: str | None = None
    agent_id: str


@router.delete("/sessions/{session_id}")
async def cleanup_session(session_id: str, body: _CleanupBody):
    """Tell the runtime to drop its workspace entry for this (agent, session).

    Safe to hit any pod in the env — the RWX PVC means every pod sees the
    same `/workspace-root/sessions/{sid}/agents/{aid}/` directory.
    """
    base = resolve_runtime_base(body.runtime_endpoint)
    try:
        async with httpx.AsyncClient() as client:
            resp = await client.delete(
                f"{base}/sessions/{session_id}",
                params={"agent_id": body.agent_id},
                timeout=5,
            )
            return {"ok": resp.status_code in (200, 404)}
    except httpx.HTTPError:
        logger.warning("Cleanup failed for session %s", session_id, exc_info=True)
        return {"ok": False}
