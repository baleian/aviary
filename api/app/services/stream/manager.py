"""Drive a single agent turn from the WS handler.

Flow:
  1. Build the on-the-wire agent_config (full spec — runtime_endpoint,
     model_config, instruction, tools, mcp_servers, accessible_agents).
  2. POST to supervisor /v1/sessions/{sid}/message. Block until the
     supervisor finishes streaming + assembly.
  3. Persist the assembled agent response to the DB.

The supervisor owns every Redis write that happens as part of the turn:
the stream_id is allocated there, events stream live via
`session:{sid}:events`, chunks buffer under `stream:{sid}:chunks`, and
stream/session status keys are managed there. The API only reads.
"""

import asyncio
import base64
import logging
import uuid

from sqlalchemy import select

from app.db.session import async_session_factory
from app.services import agent_supervisor, session_service
from aviary_shared.db.models import FileUpload

logger = logging.getLogger(__name__)

# session_id → running turn task (serializes concurrent messages per session
# at the API level; the supervisor enforces the same invariant on its side).
_active_streams: dict[str, asyncio.Task] = {}
# session_id → stream_id last dispatched; used to target the /abort call
# without having to remember it on the WS side.
_latest_stream: dict[str, str] = {}


async def start_stream(
    session_id: str,
    agent_config: dict,
    content: str,
    user_message_id: uuid.UUID,
    user_token: str,
    attachments: list[dict] | None = None,
) -> None:
    existing = _active_streams.get(session_id)
    if existing and not existing.done():
        logger.warning("Cancelling existing stream for session %s", session_id)
        existing.cancel()

    task = asyncio.create_task(
        _run_stream(session_id, agent_config, content, user_message_id, user_token, attachments)
    )
    _active_streams[session_id] = task

    def _cleanup(_: asyncio.Task) -> None:
        _active_streams.pop(session_id, None)
        _latest_stream.pop(session_id, None)

    task.add_done_callback(_cleanup)


def is_streaming(session_id: str) -> bool:
    task = _active_streams.get(session_id)
    return task is not None and not task.done()


async def cancel_stream(session_id: str) -> bool:
    task = _active_streams.get(session_id)
    if not task or task.done():
        return False

    stream_id = _latest_stream.get(session_id)
    if stream_id:
        await agent_supervisor.abort_stream(stream_id)

    task.cancel()
    return True


async def _build_content_parts(
    content: str, attachments: list[dict] | None,
) -> list[dict]:
    part: dict = {}
    if content:
        part["text"] = content
    if attachments:
        file_ids = [uuid.UUID(att["file_id"]) for att in attachments]
        async with async_session_factory() as db:
            uploads = {
                str(u.id): u for u in (await db.execute(
                    select(FileUpload).where(FileUpload.id.in_(file_ids))
                )).scalars().all()
            }
        resolved = [
            {
                "type": "image",
                "media_type": uploads[att["file_id"]].content_type,
                "data": base64.b64encode(uploads[att["file_id"]].data).decode("ascii"),
            }
            for att in attachments
            if att["file_id"] in uploads
        ]
        if resolved:
            part["attachments"] = resolved
    return [part] if part else []


async def _run_stream(
    session_id: str,
    agent_config: dict,
    content: str,
    user_message_id: uuid.UUID,
    user_token: str,
    attachments: list[dict] | None,
) -> None:
    session_uuid = uuid.UUID(session_id)
    content_parts = await _build_content_parts(content, attachments)

    reached_runtime = False

    try:
        result = await agent_supervisor.post_message(
            session_id=session_id,
            user_token=user_token,
            body={
                "session_id": session_id,
                "content_parts": content_parts,
                "agent_config": agent_config,
            },
        )
        stream_id = result.get("stream_id")
        if stream_id:
            _latest_stream[session_id] = stream_id

        reached_runtime = bool(result.get("reached_runtime"))
        if result.get("status") != "complete":
            raise RuntimeError(result.get("message", "Agent runtime error"))

        full_response = result.get("assembled_text", "")
        blocks_meta = result.get("assembled_blocks", [])
        meta = {"blocks": blocks_meta} if blocks_meta else None

        async with async_session_factory() as db:
            await session_service.save_message(
                db, session_uuid, "agent", full_response, metadata=meta,
            )
            await db.commit()

    except asyncio.CancelledError:
        logger.info("Stream cancelled for session %s", session_id)
    except Exception as exc:
        logger.exception("Stream failed for session %s", session_id)
        # query() was never invoked — the user message isn't in SDK conversation
        # history, so roll it back so the DB stays consistent.
        if not reached_runtime:
            try:
                async with async_session_factory() as db:
                    await session_service.delete_message(db, user_message_id)
                    await db.commit()
            except Exception:
                logger.warning("Failed to rollback user message %s", user_message_id)
        # Error event itself is emitted by the supervisor into the Redis channel;
        # no API-side publish needed.
        _ = exc
