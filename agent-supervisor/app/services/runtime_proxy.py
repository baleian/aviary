"""Runtime pool proxy — opens an SSE stream to the pool Service and fans events
out to Redis. This module is the supervisor's single reason to exist.

Two endpoint modes are supported (see config.py): in-cluster DNS vs. K8s API
service proxy. The HTTP client factory picks between them; everything else
(event loop, Redis publish, metric bookkeeping) is mode-agnostic.
"""

from __future__ import annotations

import asyncio
import json
import logging
import time
from contextlib import asynccontextmanager
from typing import AsyncIterator

import httpx

from app import redis_client
from app.config import settings
from app.metrics import (
    active_streams,
    errors_total,
    events_total,
    redis_publish_duration_seconds,
    stream_duration_seconds,
)

logger = logging.getLogger(__name__)

# session_id -> Task that owns the in-flight /v1/stream request for that
# session. /v1/sessions/{sid}/abort cancels the task; cancellation closes
# the httpx connection to the pool, and the pool pod's req.on('close')
# handler aborts the SDK query on the correct replica.
_active_streams: dict[str, asyncio.Task] = {}


def _pool_service_name(pool_name: str) -> str:
    # Keep in sync with pool YAML naming in k8s/platform/pools/.
    return f"env-{pool_name}"


def _pool_base_url(pool_name: str) -> str:
    service = _pool_service_name(pool_name)
    return f"http://{service}.{settings.agents_namespace}.svc:{settings.runtime_pool_port}"


@asynccontextmanager
async def _open_stream(
    pool_name: str, subpath: str, body: dict,
) -> AsyncIterator[httpx.Response]:
    """Yield an httpx streaming response to the pool Service via in-cluster DNS."""
    base = _pool_base_url(pool_name)
    async with httpx.AsyncClient(timeout=None) as client:
        async with client.stream("POST", f"{base}/{subpath.lstrip('/')}", json=body) as resp:
            yield resp


async def passthrough_from_pool(
    *,
    pool_name: str,
    session_id: str,
    agent_id: str,
    body: dict,
) -> AsyncIterator[bytes]:
    """Yield raw SSE bytes from the pool Service — no Redis publish, no metric
    bookkeeping. Used by A2A, whose caller (the parent API's a2a router) does
    its own selective re-publishing.
    """
    forward_body = {**body, "session_id": session_id, "agent_id": agent_id}
    try:
        async with _open_stream(pool_name, "/message", forward_body) as resp:
            if resp.status_code != 200:
                err = (await resp.aread()).decode(errors="replace")[:500]
                logger.error(
                    "passthrough %d for pool %s session %s: %s",
                    resp.status_code, pool_name, session_id, err,
                )
                errors_total.labels(pool_name, "runtime").inc()
                error_event = json.dumps(
                    {"type": "error", "message": f"Agent runtime error ({resp.status_code}): {err}"}
                )
                yield f"data: {error_event}\n\n".encode()
                return
            async for raw in resp.aiter_raw():
                yield raw
    except httpx.HTTPError as e:
        logger.exception("passthrough HTTP error for pool=%s session=%s", pool_name, session_id)
        errors_total.labels(pool_name, "http").inc()
        error_event = json.dumps({"type": "error", "message": f"Agent runtime connection failed: {e}"})
        yield f"data: {error_event}\n\n".encode()


async def stream_from_pool(
    *,
    pool_name: str,
    session_id: str,
    agent_id: str,
    body: dict,
) -> dict:
    """Consume runtime SSE, publish each event to Redis, return final status.

    Caller (API server / workflow worker) blocks on this call until the stream
    completes. The event stream itself is not returned — subscribers get events
    via the `session:{id}:messages` Redis channel.
    """
    forward_body = {**body, "session_id": session_id, "agent_id": agent_id}
    reached_runtime = False
    error_message: str | None = None
    result_meta: dict | None = None
    status_label = "complete"
    was_aborted = False

    active_streams.labels(pool_name).inc()
    start = time.perf_counter()

    current_task = asyncio.current_task()
    if current_task is not None:
        _active_streams[session_id] = current_task

    try:
        async with _open_stream(pool_name, "/message", forward_body) as resp:
            if resp.status_code != 200:
                err = (await resp.aread()).decode(errors="replace")[:500]
                logger.error("Runtime stream %d for pool %s: %s", resp.status_code, pool_name, err)
                errors_total.labels(pool_name, "runtime").inc()
                error_message = f"Agent runtime error ({resp.status_code}): {err}"
            else:
                async for line in resp.aiter_lines():
                    if not line.startswith("data: "):
                        continue
                    event = json.loads(line[6:])
                    etype = event.get("type", "unknown")
                    events_total.labels(pool_name, etype).inc()

                    if etype == "query_started":
                        reached_runtime = True
                        continue
                    if etype == "error":
                        error_message = event.get("message", "Agent runtime error")
                        errors_total.labels(pool_name, "runtime").inc()
                        break
                    if etype == "result":
                        result_meta = event

                    publish_start = time.perf_counter()
                    try:
                        await redis_client.append_stream_chunk(session_id, event)
                        await redis_client.publish_message(session_id, event)
                    except Exception:
                        errors_total.labels(pool_name, "redis").inc()
                        raise
                    finally:
                        redis_publish_duration_seconds.observe(time.perf_counter() - publish_start)
    except asyncio.CancelledError:
        # Triggered by /v1/sessions/{sid}/abort cancelling this task. Closing
        # the async context manager above tears down the httpx stream, which
        # closes the TCP socket to the pool pod; the pool pod's
        # req.on('close') then fires abortController.abort() on the correct
        # replica. We swallow the CancelledError so /v1/stream returns a clean
        # JSON summary to the caller instead of a 500.
        was_aborted = True
        logger.info("Stream for pool=%s session=%s aborted by caller", pool_name, session_id)
    except httpx.HTTPError as e:
        logger.exception("Runtime stream HTTP error for pool=%s session=%s", pool_name, session_id)
        errors_total.labels(pool_name, "http").inc()
        error_message = f"Agent runtime connection failed: {e}"
    except Exception:
        logger.exception("Unexpected runtime stream error for pool=%s session=%s", pool_name, session_id)
        errors_total.labels(pool_name, "unknown").inc()
        raise
    finally:
        active_streams.labels(pool_name).dec()
        # Release the task slot whether we completed or were aborted.
        if _active_streams.get(session_id) is current_task:
            _active_streams.pop(session_id, None)

    if was_aborted:
        status_label = "aborted"
        await redis_client.set_stream_status(session_id, "aborted")
        stream_duration_seconds.labels(pool_name, status_label).observe(time.perf_counter() - start)
        return {
            "status": "aborted",
            "reached_runtime": reached_runtime,
        }

    if error_message:
        status_label = "error"
        await redis_client.set_stream_status(session_id, "error")
        stream_duration_seconds.labels(pool_name, status_label).observe(time.perf_counter() - start)
        return {
            "status": "error",
            "message": error_message,
            "reached_runtime": reached_runtime,
        }

    await redis_client.set_stream_status(session_id, "complete")
    stream_duration_seconds.labels(pool_name, status_label).observe(time.perf_counter() - start)
    return {
        "status": "complete",
        "reached_runtime": reached_runtime,
        "result_meta": result_meta,
    }


async def abort_session(*, pool_name: str, session_id: str) -> dict:
    """Abort an in-flight /v1/stream by cancelling its owning asyncio Task.

    Cancellation propagates into the httpx streaming context, which closes the
    TCP connection to the correct pool pod. That pod's req.on('close') handler
    calls abortController.abort() on the SDK query. Works regardless of which
    pool replica is serving the session — unlike the old fire-and-forget
    `POST /abort/:sid`, which the pool Service would load-balance to a random
    replica that almost never held the target session.
    """
    task = _active_streams.get(session_id)
    if task is None or task.done():
        return {"ok": False, "reason": "no_active_stream"}
    task.cancel()
    return {"ok": True}


async def cleanup_session(*, pool_name: str, session_id: str, agent_id: str) -> dict:
    """Tell the pool pod to delete this session's workspace subtree."""
    base = _pool_base_url(pool_name)
    url = f"{base}/sessions/{session_id}/workspace?agent_id={agent_id}"
    try:
        async with httpx.AsyncClient(timeout=settings.runtime_pool_request_timeout) as client:
            resp = await client.delete(url)
        return {"ok": True, "status": resp.status_code}
    except httpx.HTTPError:
        logger.info(
            "Session cleanup skipped for pool=%s session=%s (not reachable)",
            pool_name, session_id,
        )
        return {"ok": False, "reason": "pool_not_reachable"}
