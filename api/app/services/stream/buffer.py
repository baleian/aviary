"""Redis-backed stream buffer + pub/sub, keyed by opaque `stream_id`.

Today one WebSocket ⇄ one session ⇄ one agent, so callers pass
`stream_id = session_id`. Keeping the abstraction opaque lets a future
workflow runtime use composite ids (e.g. f"{session_id}:{step_id}")
without touching this layer.

Key layout (per stream_id):
    stream:{stream_id}:channel   — Pub/Sub fan-out
    stream:{stream_id}:buffer    — List[JSON event], 10 min TTL, for reconnect replay
    stream:{stream_id}:status    — "streaming" | "complete" | "error"
    stream:{stream_id}:result    — JSON {content, messageId} set on successful done
"""

import json
from typing import Any

from app.deps import get_redis

BUFFER_TTL = 600


def _k(stream_id: str, suffix: str) -> str:
    return f"stream:{stream_id}:{suffix}"


def channel(stream_id: str) -> str:
    return _k(stream_id, "channel")


def control_channel(stream_id: str) -> str:
    """Pub/Sub channel for cross-pod control signals (e.g. cancel).
    Only the owning pod (lease holder) subscribes; any pod may publish."""
    return _k(stream_id, "control")


async def publish_control(stream_id: str, signal: dict) -> None:
    await get_redis().publish(control_channel(stream_id), json.dumps(signal))


async def publish(stream_id: str, event: dict[str, Any]) -> None:
    await get_redis().publish(channel(stream_id), json.dumps(event))


async def append(stream_id: str, event: dict[str, Any]) -> None:
    r = get_redis()
    key = _k(stream_id, "buffer")
    await r.rpush(key, json.dumps(event))
    await r.expire(key, BUFFER_TTL)


async def publish_and_append(stream_id: str, event: dict[str, Any]) -> None:
    await append(stream_id, event)
    await publish(stream_id, event)


async def read_buffer(stream_id: str) -> list[dict[str, Any]]:
    raw = await get_redis().lrange(_k(stream_id, "buffer"), 0, -1)
    return [json.loads(r) for r in raw]


async def clear_buffer(stream_id: str) -> None:
    r = get_redis()
    await r.delete(_k(stream_id, "buffer"), _k(stream_id, "status"), _k(stream_id, "result"))


async def set_status(stream_id: str, status: str) -> None:
    await get_redis().set(_k(stream_id, "status"), status, ex=BUFFER_TTL)


async def get_status(stream_id: str) -> str | None:
    return await get_redis().get(_k(stream_id, "status"))


async def set_result(stream_id: str, content: str, message_id: str) -> None:
    await get_redis().set(
        _k(stream_id, "result"),
        json.dumps({"content": content, "messageId": message_id}),
        ex=BUFFER_TTL,
    )


async def get_result(stream_id: str) -> dict[str, Any] | None:
    raw = await get_redis().get(_k(stream_id, "result"))
    return json.loads(raw) if raw else None
