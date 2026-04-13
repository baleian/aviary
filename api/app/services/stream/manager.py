"""Stream lifecycle — cross-pod-safe.

All authoritative state lives in Redis; no process-local dicts. Any API
replica can service a new message / cancel / reconnect for any stream_id.

Coordination primitives:
  * `stream/lock.py` — exclusive lease on `stream_id` with fencing token.
  * `stream/buffer.py` — Pub/Sub + replay buffer keyed by `stream_id`.
  * supervisor `/abort` — cross-pod cancel; breaks the owner's SSE loop,
    whose `finally` releases the lease.

Semantics:
  * A new message on a busy stream **preempts** the previous one
    (cancel-and-replace). Within a bounded grace window the lease must
    be relinquished by the old owner; otherwise the request fails with
    `stream.busy`.
  * The pod that holds the lease also runs a heartbeat loop that refreshes
    the TTL. A crashed owner's lease simply expires; a subsequent request
    clears the stale buffer before starting fresh.
"""

import asyncio
import contextlib
import json
import logging
import uuid
from dataclasses import dataclass

from fastapi import HTTPException
from sqlalchemy import select

from app.deps import db_factory, get_redis
from app.identity import instance_id
from app.services import sessions as session_svc
from app.services.stream import buffer, events
from app.services.stream.lock import get_lock
from app.services.supervisor import supervisor_client
from aviary_shared.db.models import Agent

logger = logging.getLogger(__name__)

TOOL_RESULT_MAX = 10_000

LEASE_TTL_SECONDS = 15
HEARTBEAT_INTERVAL_SECONDS = 5
PREEMPT_GRACE_SECONDS = 3.0
PREEMPT_POLL_INTERVAL = 0.1


@dataclass
class StreamRequest:
    stream_id: str
    agent_id: str
    content: str
    user_message_id: uuid.UUID
    # Optional runtime override — sent verbatim as `agent_config.mock_scenario`.
    # Request-scoped (never persisted); lets tests script deterministic SSE
    # without invoking an LLM. Runtime checks presence to dispatch mock path.
    mock_scenario: dict | None = None


async def is_streaming(stream_id: str) -> bool:
    return (await get_lock().owner_of(stream_id)) is not None


async def start(req: StreamRequest) -> None:
    """Preempt any existing stream for this stream_id, then start a new one.

    Raises HTTPException(409) if the previous owner does not release its
    lease within the grace window.
    """
    lock = get_lock()
    holder_id = instance_id()

    # Preempt: if someone currently owns the lease, signal cancel so the
    # owner emits `cancelled` + releases the lease, then wait for release.
    existing = await lock.owner_of(req.stream_id)
    if existing is not None:
        await cancel(req.stream_id, req.agent_id)
        if not await _wait_for_release(lock, req.stream_id, PREEMPT_GRACE_SECONDS):
            raise HTTPException(409, "stream.busy: previous stream did not release within grace")

    # A stale lease (crashed pod) can leave the buffer in "streaming"; always
    # start from a clean slate.
    await buffer.clear_buffer(req.stream_id)

    if not await lock.acquire(req.stream_id, holder_id, LEASE_TTL_SECONDS):
        raise HTTPException(409, "stream.busy: another writer acquired the lease first")

    # Spawn the worker; it owns releasing the lease + stopping the heartbeat.
    asyncio.create_task(_run(req, holder_id))


async def cancel(stream_id: str, agent_id: str) -> None:
    """Cross-pod cancel.

    Publishes a `cancel` signal on the stream's control channel (only the
    owning pod listens); the owner then aborts the supervisor-side stream
    and surfaces a `cancelled` event. We also abort directly as a fallback
    in case no pod currently owns the lease (stream already finished).
    """
    await buffer.publish_control(stream_id, {"reason": "cancel"})
    await supervisor_client.abort_session(agent_id, stream_id)


async def _wait_for_release(lock, stream_id: str, timeout_s: float) -> bool:
    deadline = asyncio.get_event_loop().time() + timeout_s
    while asyncio.get_event_loop().time() < deadline:
        if await lock.owner_of(stream_id) is None:
            return True
        await asyncio.sleep(PREEMPT_POLL_INTERVAL)
    return False


async def _heartbeat(stream_id: str, holder_id: str) -> None:
    lock = get_lock()
    try:
        while True:
            await asyncio.sleep(HEARTBEAT_INTERVAL_SECONDS)
            if not await lock.refresh(stream_id, holder_id, LEASE_TTL_SECONDS):
                # We no longer hold the lease (preempted or TTL expired) —
                # stop refreshing silently; the worker's next Redis op or
                # the supervisor abort will surface the state.
                logger.warning("Lease lost for stream %s (holder %s)", stream_id, holder_id)
                return
    except asyncio.CancelledError:
        return


async def _load_agent(agent_id: str) -> Agent | None:
    async with db_factory()() as db:
        row = await db.execute(select(Agent).where(Agent.id == uuid.UUID(agent_id)))
        return row.scalar_one_or_none()


async def _rollback_user_message(user_message_id: uuid.UUID) -> None:
    async with db_factory()() as db:
        await session_svc.delete_message(db, user_message_id)
        await db.commit()


async def _run(req: StreamRequest, holder_id: str) -> None:
    stream_id = req.stream_id
    agent_id = req.agent_id
    lock = get_lock()

    heartbeat = asyncio.create_task(_heartbeat(stream_id, holder_id))

    try:
        await _drive_stream(req)
    finally:
        heartbeat.cancel()
        try:
            await heartbeat
        except asyncio.CancelledError:
            pass
        await lock.release(stream_id, holder_id)


async def _control_watcher(stream_id: str, agent_id: str, cancelled: asyncio.Event) -> None:
    """Subscribe to the stream's control channel; on `cancel` set the flag
    and abort the supervisor-side stream (which breaks our SSE loop)."""
    pubsub = get_redis().pubsub()
    try:
        await pubsub.subscribe(buffer.control_channel(stream_id))
        async for msg in pubsub.listen():
            if msg.get("type") != "message":
                continue
            try:
                data = json.loads(msg["data"])
            except (ValueError, TypeError):
                continue
            if data.get("reason") == "cancel":
                cancelled.set()
                await supervisor_client.abort_session(agent_id, stream_id)
                return
    except asyncio.CancelledError:
        pass
    finally:
        with contextlib.suppress(Exception):
            await pubsub.unsubscribe()
            await pubsub.aclose()


async def _drive_stream(req: StreamRequest) -> None:
    stream_id = req.stream_id
    agent_id = req.agent_id

    await buffer.set_status(stream_id, "streaming")

    agent = await _load_agent(agent_id)
    if agent is None:
        await _fail_pre_runtime(req, "Agent not found")
        return

    ready = await supervisor_client.wait_ready(agent_id)
    if not ready:
        await _fail_pre_runtime(req, "Agent did not become ready in time")
        return

    agent_config: dict = {
        "instruction": agent.instruction or "",
        "tools": agent.tools or [],
        "mcp_servers": {},
        "policy": agent.policy.policy_rules if agent.policy else {},
        "user_external_id": "",
        "user_token": "",
    }
    if req.mock_scenario:
        agent_config["mock_scenario"] = req.mock_scenario

    supervisor_body = {
        "content_parts": [{"text": req.content}],
        "session_id": stream_id,
        "model_config_data": agent.model_config_data or {},
        "agent_config": agent_config,
    }

    full_text = ""
    blocks: list[dict] = []
    pending_text = ""
    pending_thinking = ""
    tool_results: dict[str, dict] = {}
    reached_runtime = False
    cancelled = asyncio.Event()
    ctrl_task = asyncio.create_task(_control_watcher(stream_id, agent_id, cancelled))

    def _flush_pending() -> None:
        nonlocal pending_text, pending_thinking
        if pending_thinking:
            blocks.append({"type": "thinking", "content": pending_thinking})
            pending_thinking = ""
        if pending_text:
            blocks.append({"type": "text", "content": pending_text})
            pending_text = ""

    try:
        async for data_line in supervisor_client.stream_message(agent_id, stream_id, supervisor_body):
            event = json.loads(data_line)
            etype = event.get("type")

            if etype == events.QUERY_STARTED:
                reached_runtime = True
                continue

            if etype == events.CHUNK:
                text = event.get("content", "")
                if pending_thinking:
                    blocks.append({"type": "thinking", "content": pending_thinking})
                    pending_thinking = ""
                pending_text += text
                full_text += text
                await buffer.publish_and_append(stream_id, {"type": events.CHUNK, "content": text})

            elif etype == events.THINKING:
                pending_thinking += event.get("content", "")
                await buffer.publish_and_append(stream_id, event)

            elif etype == events.TOOL_USE:
                _flush_pending()
                blocks.append({
                    "type": "tool_call",
                    "name": event.get("name"),
                    "input": event.get("input"),
                    "tool_use_id": event.get("tool_use_id"),
                })
                await buffer.publish_and_append(stream_id, event)

            elif etype == events.TOOL_RESULT:
                tid = event.get("tool_use_id")
                content = (event.get("content") or "")
                if len(content) > TOOL_RESULT_MAX:
                    content = content[:TOOL_RESULT_MAX] + "\n... (truncated)"
                if tid:
                    tool_results[tid] = {"content": content, "is_error": event.get("is_error", False)}
                await buffer.publish_and_append(stream_id, {**event, "content": content})

            elif etype == events.TOOL_PROGRESS:
                await buffer.publish(stream_id, event)

            elif etype == events.ERROR:
                raise RuntimeError(event.get("message") or "Agent runtime error")

        _flush_pending()
        _attach_tool_results(blocks, tool_results)

        if cancelled.is_set():
            # User-initiated cancel: persist the partial, emit `cancelled`.
            metadata: dict | None = {"blocks": blocks, "cancelled": True} if blocks else {"cancelled": True}
            async with db_factory()() as db:
                msg = await session_svc.save_message(
                    db, uuid.UUID(stream_id), "agent",
                    full_text or "[cancelled]", metadata=metadata,
                )
                await db.commit()
                message_id = str(msg.id)
            await buffer.set_status(stream_id, "complete")
            await buffer.publish(stream_id, {"type": events.CANCELLED, "messageId": message_id})
        else:
            metadata = {"blocks": blocks} if blocks else None
            async with db_factory()() as db:
                msg = await session_svc.save_message(
                    db, uuid.UUID(stream_id), "agent", full_text, metadata=metadata,
                )
                await db.commit()
                message_id = str(msg.id)
            await buffer.set_status(stream_id, "complete")
            await buffer.set_result(stream_id, full_text, message_id)
            await buffer.publish(stream_id, {"type": events.DONE, "messageId": message_id})

    except asyncio.CancelledError:
        logger.info("Stream task cancelled for %s", stream_id)
        await buffer.set_status(stream_id, "error")
        await buffer.publish(stream_id, {"type": events.CANCELLED})
        raise
    except Exception as exc:
        logger.exception("Stream failed for %s", stream_id)
        await buffer.set_status(stream_id, "error")
        err = {"type": events.ERROR, "message": str(exc) or "Agent streaming failed"}
        if not reached_runtime:
            try:
                await _rollback_user_message(req.user_message_id)
                err["rollback_message_id"] = str(req.user_message_id)
            except Exception:
                logger.warning("Rollback failed for user message %s", req.user_message_id)
        await buffer.publish(stream_id, err)
    finally:
        ctrl_task.cancel()
        with contextlib.suppress(asyncio.CancelledError):
            await ctrl_task


async def _fail_pre_runtime(req: StreamRequest, reason: str) -> None:
    try:
        await _rollback_user_message(req.user_message_id)
    except Exception:
        logger.warning("Rollback failed for user message %s", req.user_message_id)
    await buffer.set_status(req.stream_id, "error")
    await buffer.publish(req.stream_id, {
        "type": events.ERROR,
        "message": reason,
        "rollback_message_id": str(req.user_message_id),
    })


def _attach_tool_results(blocks: list[dict], tool_results: dict[str, dict]) -> None:
    for block in blocks:
        if block.get("type") == "tool_call":
            tid = block.get("tool_use_id")
            if tid and tid in tool_results:
                block["result"] = tool_results[tid]["content"]
                if tool_results[tid].get("is_error"):
                    block["is_error"] = True
