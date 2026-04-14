"""Session-level status + per-user unread counters for the sidebar.

State lives in Redis so any API replica can read/mutate it uniformly.
Stream events drive the status ("streaming"/"idle"); WS connect/ack drive
unread reset; WS broadcast drives unread increment for non-active viewers.

Status values:
  "streaming" — an LLM turn is in flight
  "idle"      — session is live but not streaming
  "offline"   — (implicit) no status key set, agent never warmed or expired
"""

from __future__ import annotations

from app.deps import get_redis

_STATUS_TTL = 3600
_UNREAD_TTL = 86400


def _status_key(session_id: str) -> str:
    return f"session:{session_id}:status"


def _unread_key(session_id: str, user_id: str) -> str:
    return f"session:{session_id}:unread:{user_id}"


async def set_status(session_id: str, status: str) -> None:
    await get_redis().set(_status_key(session_id), status, ex=_STATUS_TTL)


async def get_bulk_status(session_ids: list[str]) -> dict[str, str]:
    if not session_ids:
        return {}
    pipe = get_redis().pipeline()
    for sid in session_ids:
        pipe.get(_status_key(sid))
    results = await pipe.execute()
    return {sid: (r or "offline") for sid, r in zip(session_ids, results)}


async def increment_unread(session_id: str, user_id: str) -> None:
    redis = get_redis()
    key = _unread_key(session_id, user_id)
    await redis.incr(key)
    await redis.expire(key, _UNREAD_TTL)


async def clear_unread(session_id: str, user_id: str) -> None:
    await get_redis().delete(_unread_key(session_id, user_id))


async def get_bulk_unread(session_ids: list[str], user_id: str) -> dict[str, int]:
    if not session_ids:
        return {}
    pipe = get_redis().pipeline()
    for sid in session_ids:
        pipe.get(_unread_key(sid, user_id))
    results = await pipe.execute()
    return {sid: int(r) if r else 0 for sid, r in zip(session_ids, results)}
