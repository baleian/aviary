"""Background stream lifecycle: start, cancel, run.

Design decisions vs. v1:
- No accessible_agents / credentials / attachments / mentions (out of MVP).
- Persisted content is the raw concatenated text; we also store a `blocks`
  metadata array for future rich-rendering. No A2A merging.
- User-message rollback only when query() was never invoked (preserves
  SDK conversation-history consistency).
"""

import asyncio
import json
import logging
import uuid
from dataclasses import dataclass

import httpx
from sqlalchemy import select

from app.deps import db_factory
from app.services import sessions as session_svc
from app.services.stream import buffer, events
from app.services.supervisor import supervisor_client
from aviary_shared.db.models import Agent

logger = logging.getLogger(__name__)

_active: dict[str, asyncio.Task] = {}

TOOL_RESULT_MAX = 10_000


@dataclass
class StreamRequest:
    session_id: str
    agent_id: str
    content: str
    user_message_id: uuid.UUID
    # Optional runtime override — sent verbatim as `agent_config.mock_scenario`.
    # Request-scoped (never persisted); lets tests script deterministic SSE
    # without invoking an LLM. Runtime checks presence to dispatch mock path.
    mock_scenario: dict | None = None


def is_streaming(session_id: str) -> bool:
    task = _active.get(session_id)
    return task is not None and not task.done()


async def start(req: StreamRequest) -> None:
    prev = _active.get(req.session_id)
    if prev and not prev.done():
        prev.cancel()
    await buffer.clear_buffer(req.session_id)
    task = asyncio.create_task(_run(req))
    _active[req.session_id] = task
    task.add_done_callback(lambda _t: _active.pop(req.session_id, None))


async def cancel(session_id: str, agent_id: str) -> bool:
    task = _active.get(session_id)
    if not task or task.done():
        return False
    await supervisor_client.abort_session(agent_id, session_id)
    task.cancel()
    return True


async def _load_agent(agent_id: str) -> Agent | None:
    async with db_factory()() as db:
        row = await db.execute(select(Agent).where(Agent.id == uuid.UUID(agent_id)))
        return row.scalar_one_or_none()


async def _rollback_user_message(user_message_id: uuid.UUID) -> None:
    async with db_factory()() as db:
        await session_svc.delete_message(db, user_message_id)
        await db.commit()


async def _run(req: StreamRequest) -> None:
    session_id = req.session_id
    agent_id = req.agent_id

    await buffer.set_status(session_id, "streaming")

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
        "session_id": session_id,
        "model_config_data": agent.model_config_data or {},
        "agent_config": agent_config,
    }

    full_text = ""
    blocks: list[dict] = []
    pending_text = ""
    pending_thinking = ""
    tool_results: dict[str, dict] = {}
    reached_runtime = False

    def _flush_pending() -> None:
        nonlocal pending_text, pending_thinking
        if pending_thinking:
            blocks.append({"type": "thinking", "content": pending_thinking})
            pending_thinking = ""
        if pending_text:
            blocks.append({"type": "text", "content": pending_text})
            pending_text = ""

    try:
        async for data_line in supervisor_client.stream_message(agent_id, session_id, supervisor_body):
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
                await buffer.publish_and_append(session_id, {"type": events.CHUNK, "content": text})

            elif etype == events.THINKING:
                pending_thinking += event.get("content", "")
                await buffer.publish_and_append(session_id, event)

            elif etype == events.TOOL_USE:
                _flush_pending()
                blocks.append({
                    "type": "tool_call",
                    "name": event.get("name"),
                    "input": event.get("input"),
                    "tool_use_id": event.get("tool_use_id"),
                })
                await buffer.publish_and_append(session_id, event)

            elif etype == events.TOOL_RESULT:
                tid = event.get("tool_use_id")
                content = (event.get("content") or "")
                if len(content) > TOOL_RESULT_MAX:
                    content = content[:TOOL_RESULT_MAX] + "\n... (truncated)"
                if tid:
                    tool_results[tid] = {"content": content, "is_error": event.get("is_error", False)}
                await buffer.publish_and_append(session_id, {**event, "content": content})

            elif etype == events.TOOL_PROGRESS:
                await buffer.publish(session_id, event)

            elif etype == events.ERROR:
                raise RuntimeError(event.get("message") or "Agent runtime error")

        _flush_pending()
        _attach_tool_results(blocks, tool_results)

        metadata = {"blocks": blocks} if blocks else None
        async with db_factory()() as db:
            msg = await session_svc.save_message(
                db, uuid.UUID(session_id), "agent", full_text, metadata=metadata,
            )
            await db.commit()
            message_id = str(msg.id)

        await buffer.set_status(session_id, "complete")
        await buffer.set_result(session_id, full_text, message_id)
        await buffer.publish(session_id, {"type": events.DONE, "messageId": message_id})

    except asyncio.CancelledError:
        logger.info("Stream cancelled for session %s", session_id)
        await buffer.set_status(session_id, "error")
        await buffer.publish(session_id, {"type": events.CANCELLED})
        raise
    except Exception as exc:
        logger.exception("Stream failed for session %s", session_id)
        await buffer.set_status(session_id, "error")
        err = {"type": events.ERROR, "message": str(exc) or "Agent streaming failed"}
        if not reached_runtime:
            try:
                await _rollback_user_message(req.user_message_id)
                err["rollback_message_id"] = str(req.user_message_id)
            except Exception:
                logger.warning("Rollback failed for user message %s", req.user_message_id)
        await buffer.publish(session_id, err)


async def _fail_pre_runtime(req: StreamRequest, reason: str) -> None:
    try:
        await _rollback_user_message(req.user_message_id)
    except Exception:
        logger.warning("Rollback failed for user message %s", req.user_message_id)
    await buffer.set_status(req.session_id, "error")
    await buffer.publish(req.session_id, {
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
