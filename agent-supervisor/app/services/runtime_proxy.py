"""Runtime pool proxy — opens an SSE stream to the pool Service and fans events
out to Redis. This module is the supervisor's single reason to exist.

Each stream is identified by a caller-supplied `stream_id` (UUID). The flow:

    /v1/stream request lands on supervisor-X
        └─▶ opens POST /message to env-{pool}.agents.svc (K8s LB picks runtime-pod-N)
            └─▶ runtime-pod-N returns X-Runtime-Pod-IP header with its own POD_IP
                └─▶ supervisor-X writes stream:{stream_id}:runtime_addr = "POD_IP:port" to Redis

    /v1/streams/{stream_id}/abort lands on any supervisor replica
        └─▶ reads stream:{stream_id}:runtime_addr from Redis
            └─▶ POSTs directly to http://POD_IP:port/abort/{session_id}  (bypassing the Service)
                └─▶ runtime-pod-N cancels the SDK query for that session

Supervisor carries no cross-request state; every replica can handle any abort.
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
    stream_id: str,
    body: dict,
) -> dict:
    """Consume runtime SSE, publish each event to Redis, return final status.

    Captures the serving runtime pod's IP (X-Runtime-Pod-IP header) and stores
    `stream:{stream_id}:runtime_addr` in Redis so any supervisor replica can
    route a later abort to the exact pod.
    """
    forward_body = {**body, "session_id": session_id, "agent_id": agent_id}
    reached_runtime = False
    error_message: str | None = None
    result_meta: dict | None = None
    status_label = "complete"
    was_aborted = False
    runtime_addr_recorded = False

    active_streams.labels(pool_name).inc()
    start = time.perf_counter()

    try:
        async with _open_stream(pool_name, "/message", forward_body) as resp:
            # Record the runtime pod address in Redis so abort requests can
            # dial it directly. Done before consuming the stream so an abort
            # arriving early still finds the mapping.
            pod_ip = resp.headers.get("x-runtime-pod-ip")
            if pod_ip:
                await redis_client.set_stream_runtime_addr(
                    stream_id,
                    f"{pod_ip}:{settings.runtime_pool_port}",
                    settings.stream_runtime_ttl_seconds,
                )
                runtime_addr_recorded = True
            else:
                logger.warning(
                    "Runtime pod for stream %s did not advertise X-Runtime-Pod-IP — "
                    "abort will fail to route. Check pool Deployment downward API.",
                    stream_id,
                )

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
        # The caller (API) closed its HTTP connection — uvicorn cancels this
        # handler task. Runtime will see req.on('close') independently and
        # abort its own SDK query; we just report the outcome.
        was_aborted = True
        logger.info("Stream task cancelled: stream_id=%s session=%s", stream_id, session_id)
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
        if runtime_addr_recorded:
            await redis_client.delete_stream_runtime_addr(stream_id)

    if was_aborted:
        status_label = "aborted"
        await redis_client.set_stream_status(session_id, "aborted")
        stream_duration_seconds.labels(pool_name, status_label).observe(time.perf_counter() - start)
        return {"status": "aborted", "reached_runtime": reached_runtime}

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


async def abort_stream(*, stream_id: str, session_id: str) -> dict:
    """Abort by dialing the owning runtime pod directly.

    Any supervisor replica can service this — the runtime pod address is in
    Redis, the pod's /abort endpoint uses its local activeAbortControllers
    map (keyed by session_id) to cancel the exact in-flight SDK query.
    """
    addr = await redis_client.get_stream_runtime_addr(stream_id)
    if not addr:
        return {"ok": False, "reason": "no_active_stream"}

    url = f"http://{addr}/abort/{session_id}"
    try:
        async with httpx.AsyncClient(timeout=settings.runtime_pool_request_timeout) as client:
            resp = await client.post(url)
    except httpx.HTTPError:
        logger.warning(
            "Runtime abort failed for stream=%s addr=%s (pod likely dead)",
            stream_id, addr, exc_info=True,
        )
        # Reap the stale mapping so subsequent aborts don't retry the dead pod.
        await redis_client.delete_stream_runtime_addr(stream_id)
        return {"ok": False, "reason": "runtime_unreachable", "runtime_addr": addr}

    if resp.status_code == 200:
        return {"ok": True, "runtime_addr": addr}
    if resp.status_code == 404:
        # Runtime no longer has the session (already finished / drained).
        return {"ok": False, "reason": "session_not_on_runtime", "runtime_addr": addr}
    return {"ok": False, "reason": f"runtime_http_{resp.status_code}", "runtime_addr": addr}


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
