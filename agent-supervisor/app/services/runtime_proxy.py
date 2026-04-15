"""Runtime pool proxy — opens an SSE stream to the pool Service and fans events
out to Redis. This module is the supervisor's single reason to exist.

Two endpoint modes are supported (see config.py): in-cluster DNS vs. K8s API
service proxy. The HTTP client factory picks between them; everything else
(event loop, Redis publish, metric bookkeeping) is mode-agnostic.
"""

from __future__ import annotations

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


@asynccontextmanager
async def _open_stream(
    pool_name: str, subpath: str, body: dict,
) -> AsyncIterator[httpx.Response]:
    """Yield an httpx streaming response to the pool Service, regardless of mode."""
    service = _pool_service_name(pool_name)
    if settings.runtime_pool_endpoint_mode == "k8s-proxy":
        from app.services.k8s_proxy import new_k8s_client, service_proxy_path

        path = service_proxy_path(
            settings.agents_namespace, service, settings.runtime_pool_port, subpath,
        )
        async with new_k8s_client(timeout=None) as client:
            async with client.stream("POST", path, json=body, timeout=None) as resp:
                yield resp
        return

    # direct-dns (default, in-cluster)
    base = f"http://{service}.{settings.agents_namespace}.svc:{settings.runtime_pool_port}"
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

    active_streams.labels(pool_name).inc()
    start = time.perf_counter()

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
    service = _pool_service_name(pool_name)
    subpath = f"/abort/{session_id}"
    try:
        if settings.runtime_pool_endpoint_mode == "k8s-proxy":
            from app.services.k8s_proxy import new_k8s_client, service_proxy_path

            path = service_proxy_path(
                settings.agents_namespace, service, settings.runtime_pool_port, subpath,
            )
            async with new_k8s_client(timeout=settings.runtime_pool_request_timeout) as client:
                resp = await client.post(path)
        else:
            base = f"http://{service}.{settings.agents_namespace}.svc:{settings.runtime_pool_port}"
            async with httpx.AsyncClient(timeout=settings.runtime_pool_request_timeout) as client:
                resp = await client.post(f"{base}{subpath}")
        return {"ok": True, "status": resp.status_code}
    except httpx.HTTPError:
        logger.warning("Abort failed for pool=%s session=%s", pool_name, session_id, exc_info=True)
        return {"ok": False, "reason": "pool_not_reachable"}


async def cleanup_session(*, pool_name: str, session_id: str, agent_id: str) -> dict:
    """Tell the pool pod to delete this session's workspace subtree."""
    service = _pool_service_name(pool_name)
    subpath = f"/sessions/{session_id}/workspace?agent_id={agent_id}"
    try:
        if settings.runtime_pool_endpoint_mode == "k8s-proxy":
            from app.services.k8s_proxy import new_k8s_client, service_proxy_path

            path = service_proxy_path(
                settings.agents_namespace, service, settings.runtime_pool_port, subpath,
            )
            async with new_k8s_client(timeout=settings.runtime_pool_request_timeout) as client:
                resp = await client.delete(path)
        else:
            base = f"http://{service}.{settings.agents_namespace}.svc:{settings.runtime_pool_port}"
            async with httpx.AsyncClient(timeout=settings.runtime_pool_request_timeout) as client:
                resp = await client.delete(f"{base}{subpath}")
        return {"ok": True, "status": resp.status_code}
    except httpx.HTTPError:
        logger.info(
            "Session cleanup skipped for pool=%s session=%s (not reachable)",
            pool_name, session_id,
        )
        return {"ok": False, "reason": "pool_not_reachable"}
