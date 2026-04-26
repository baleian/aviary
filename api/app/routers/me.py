import asyncio
import contextlib
import logging

from fastapi import APIRouter, WebSocket, WebSocketDisconnect
from sqlalchemy import select

from app.auth.ws import handshake_ws
from app.db.models import User
from app.db.session import async_session_factory
from app.services import redis_service

logger = logging.getLogger(__name__)

router = APIRouter()


async def _relay_user_events(websocket: WebSocket, pubsub) -> None:
    try:
        async for raw in pubsub.listen():
            if raw["type"] != "message":
                continue
            try:
                await websocket.send_text(raw["data"])
            except WebSocketDisconnect:
                return
            except Exception as exc:
                logger.debug("user-events relay send failed: %s", exc)
                return
    except asyncio.CancelledError:
        pass


@router.websocket("/events")
async def websocket_user_events(websocket: WebSocket):
    handshake = await handshake_ws(websocket)
    if handshake is None:
        return
    _, claims = handshake

    async with async_session_factory() as db:
        user = (await db.execute(
            select(User).where(User.external_id == claims.sub)
        )).scalar_one_or_none()
        if user is None:
            await websocket.close(code=4001, reason="User not found")
            return
        user_id_str = str(user.id)

    await websocket.accept()

    pubsub = await redis_service.subscribe_user(user_id_str)
    if pubsub is None:
        await websocket.send_json({"type": "error", "message": "Events backend unavailable"})
        await websocket.close(code=1011)
        return

    relay_task = asyncio.create_task(_relay_user_events(websocket, pubsub))
    try:
        # Block until the client disconnects; we don't expect any inbound
        # frames on this socket, so anything we receive is just keep-alive.
        while True:
            await websocket.receive_text()
    except WebSocketDisconnect:
        pass
    except Exception:
        logger.exception("user-events socket error for user %s", user_id_str)
    finally:
        relay_task.cancel()
        with contextlib.suppress(asyncio.CancelledError):
            await relay_task
        with contextlib.suppress(Exception):
            await pubsub.unsubscribe()
            await pubsub.aclose()
