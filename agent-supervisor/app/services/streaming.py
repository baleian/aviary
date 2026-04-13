"""SSE proxy with Redis publish — receives SSE from runtime, yields to caller,
and publishes events to Redis Pub/Sub (real-time) and Streams (durable)."""

from __future__ import annotations

import json
import logging
from typing import AsyncIterator

from aviary_shared.redis import RedisPublisher
from app.backends.protocol import RuntimeBackend

logger = logging.getLogger(__name__)

DURABLE_EVENT_TYPES = {"result", "error"}


async def proxy_and_publish(
    backend: RuntimeBackend,
    redis: RedisPublisher,
    agent_id: str,
    session_id: str,
    body: dict,
) -> AsyncIterator[bytes]:
    try:
        async for chunk in backend.stream_message(agent_id, session_id, body):
            yield chunk

            for line in chunk.decode(errors="replace").split("\n"):
                if not line.startswith("data: "):
                    continue
                try:
                    event = json.loads(line[6:])
                except (json.JSONDecodeError, IndexError):
                    continue

                await redis.publish_stream_event(session_id, event)

                event_type = event.get("type", "")
                if event_type in DURABLE_EVENT_TYPES:
                    await redis.publish_durable_event(session_id, event_type, event)

    except Exception as e:
        logger.error("Streaming error for agent %s session %s: %s", agent_id, session_id, e)
        error_event = {"type": "error", "message": str(e)}
        yield f"data: {json.dumps(error_event)}\n\n".encode()
        await redis.publish_stream_event(session_id, error_event)
        await redis.publish_durable_event(session_id, "error", error_event)
