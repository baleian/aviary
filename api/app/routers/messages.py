"""Per-session chat WebSocket.

Protocol:
  client → server: {"type": "message", "content": "..."}
                   {"type": "cancel"}
  server → client: {"type": "user_message"|"chunk"|"thinking"|"tool_use"
                   |"tool_result"|"tool_progress"|"done"|"cancelled"
                   |"error"|"replay_start"|"replay_end"|"stream_complete", ...}
"""

import asyncio
import contextlib
import logging
import uuid

from fastapi import APIRouter, WebSocket, WebSocketDisconnect

from app.auth import session_store
from app.auth.dependencies import authenticate_ws
from app.deps import db_factory
from app.services import session_status
from app.services import agents as agent_svc
from app.services import sessions as session_svc
from app.services.stream import buffer, events
from app.services.stream.manager import StreamRequest, cancel as cancel_stream, start as start_stream
from app.services.stream.relay import replay, run_relay

logger = logging.getLogger(__name__)

router = APIRouter()


@router.websocket("/sessions/{session_id}/ws")
async def websocket_chat(websocket: WebSocket, session_id: uuid.UUID):
    sid = websocket.cookies.get(session_store.COOKIE_NAME)
    async with db_factory()() as db:
        auth = await authenticate_ws(websocket, db)
        if auth is None:
            return
        user, access_token = auth
        try:
            session = await session_svc.require_owner(db, session_id, user)
        except Exception as e:
            await websocket.close(code=4003, reason=str(e))
            return
        agent = await agent_svc.get(db, session.agent_id)
        if agent is None:
            await websocket.close(code=4004, reason="Agent not found")
            return
        agent_id = str(agent.id)

    await websocket.accept()
    session_id_str = str(session_id)
    await session_status.clear_unread(session_id_str, str(user.id))

    try:
        # No pre-warm on handshake: the first message's _drive_stream
        # handles spawn + readiness, which covers both cold-start and
        # mid-session replica loss through one path.
        await websocket.send_json({"type": "status", "status": "ready"})
        await replay(websocket, session_id_str)
        relay_task = asyncio.create_task(run_relay(websocket, session_id_str, str(user.id)))

        try:
            while True:
                data = await websocket.receive_json()
                kind = data.get("type")

                if kind == "cancel":
                    await cancel_stream(session_id_str, agent_id)
                    continue

                if kind != "message":
                    continue

                content = (data.get("content") or "").strip()
                if not content:
                    continue

                # Per-turn refresh: WS outlives Keycloak's 5-min access-token TTL.
                fresh = await session_store.get_fresh(sid) if sid else None
                if fresh is None:
                    await websocket.send_json({
                        "type": events.ERROR,
                        "message": "Session expired, please sign in again",
                    })
                    await websocket.close(code=4001, reason="Session expired")
                    return
                access_token = fresh.access_token

                async with db_factory()() as db:
                    msg = await session_svc.save_message(
                        db, session_id, "user", content, sender_id=str(user.id),
                    )
                    await db.commit()
                    user_message_id = msg.id

                await buffer.publish(session_id_str, {
                    "type": events.USER_MESSAGE,
                    "sender_id": str(user.id),
                    "content": content,
                })

                try:
                    await start_stream(StreamRequest(
                        stream_id=session_id_str,
                        agent_id=agent_id,
                        content=content,
                        user_message_id=user_message_id,
                        user_token=access_token,
                        user_external_id=user.external_id,
                        mock_scenario=data.get("mock_scenario"),
                    ))
                except Exception as exc:
                    # stream.busy and other start-path failures: surface to
                    # the client without tearing down the socket.
                    await websocket.send_json({
                        "type": events.ERROR,
                        "message": str(exc.detail if hasattr(exc, "detail") else exc),
                    })

        finally:
            relay_task.cancel()
            with contextlib.suppress(asyncio.CancelledError):
                await relay_task

    except WebSocketDisconnect:
        pass
    except Exception:
        logger.exception("WebSocket error for session %s", session_id)
        with contextlib.suppress(Exception):
            await websocket.send_json({"type": events.ERROR, "message": "internal error"})
