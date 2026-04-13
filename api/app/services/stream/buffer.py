"""Redis-backed stream buffer + pub/sub for per-session messaging.

Layout per session:
  session:{id}:channel   — Pub/Sub channel (realtime fan-out)
  session:{id}:buffer    — List of JSON-encoded events (replay on reconnect)
  session:{id}:status    — "streaming" | "complete" | "error"
  session:{id}:result    — JSON {content, messageId} set on successful done
All keys share a 10-minute TTL; on stream completion the buffer is cleared.
"""

import json
from typing import Any

from app.deps import get_redis

BUFFER_TTL = 600


def _k(session_id: str, suffix: str) -> str:
    return f"session:{session_id}:{suffix}"


def channel(session_id: str) -> str:
    return _k(session_id, "channel")


async def publish(session_id: str, event: dict[str, Any]) -> None:
    await get_redis().publish(channel(session_id), json.dumps(event))


async def append(session_id: str, event: dict[str, Any]) -> None:
    r = get_redis()
    key = _k(session_id, "buffer")
    await r.rpush(key, json.dumps(event))
    await r.expire(key, BUFFER_TTL)


async def publish_and_append(session_id: str, event: dict[str, Any]) -> None:
    await append(session_id, event)
    await publish(session_id, event)


async def read_buffer(session_id: str) -> list[dict[str, Any]]:
    raw = await get_redis().lrange(_k(session_id, "buffer"), 0, -1)
    return [json.loads(r) for r in raw]


async def clear_buffer(session_id: str) -> None:
    r = get_redis()
    await r.delete(_k(session_id, "buffer"), _k(session_id, "status"), _k(session_id, "result"))


async def set_status(session_id: str, status: str) -> None:
    await get_redis().set(_k(session_id, "status"), status, ex=BUFFER_TTL)


async def get_status(session_id: str) -> str | None:
    return await get_redis().get(_k(session_id, "status"))


async def set_result(session_id: str, content: str, message_id: str) -> None:
    await get_redis().set(
        _k(session_id, "result"),
        json.dumps({"content": content, "messageId": message_id}),
        ex=BUFFER_TTL,
    )


async def get_result(session_id: str) -> dict[str, Any] | None:
    raw = await get_redis().get(_k(session_id, "result"))
    return json.loads(raw) if raw else None
