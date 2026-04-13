"""WebSocket relay + replay.

Subscribes to the per-session Pub/Sub channel and forwards JSON events
to the client. On (re)connect, replays any buffered events or the
completed result.
"""

import asyncio
import json
import logging

from fastapi import WebSocket

from app.deps import get_redis
from app.services.stream import buffer, events

logger = logging.getLogger(__name__)


async def replay(websocket: WebSocket, session_id: str) -> None:
    status = await buffer.get_status(session_id)
    if status == "streaming":
        await websocket.send_json({"type": events.REPLAY_START})
        for event in await buffer.read_buffer(session_id):
            await websocket.send_json(event)
        await websocket.send_json({"type": events.REPLAY_END})
    elif status == "complete":
        result = await buffer.get_result(session_id)
        if result:
            await websocket.send_json({
                "type": events.STREAM_COMPLETE,
                "content": result["content"],
                "messageId": result["messageId"],
            })
        await buffer.clear_buffer(session_id)


async def run_relay(websocket: WebSocket, session_id: str) -> None:
    """Subscribe to Pub/Sub and forward to the client until cancelled."""
    pubsub = get_redis().pubsub()
    try:
        await pubsub.subscribe(buffer.channel(session_id))
        async for msg in pubsub.listen():
            if msg.get("type") != "message":
                continue
            try:
                data = json.loads(msg["data"])
                await websocket.send_json(data)
            except Exception:
                logger.debug("Relay forward failed for %s", session_id, exc_info=True)
                return
    except asyncio.CancelledError:
        pass
    finally:
        try:
            await pubsub.unsubscribe()
            await pubsub.aclose()
        except Exception:
            pass
