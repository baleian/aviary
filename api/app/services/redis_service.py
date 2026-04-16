"""Redis client — read-only helpers for the API server.

The API **reads** session/stream state but never writes to Redis. All writes
are owned by the agent-supervisor (or, in the future, Temporal workers).
Keeping writes in one place makes the invariants (TTLs, key naming,
assembly ordering) easy to reason about.

Exception: `delete_all_session_keys` is a best-effort cleanup used from the
session-delete path. It's a DEL, not a stateful write — if the supervisor
is simultaneously streaming into a deleted session, both the DB delete and
this DEL race, and either order is fine.
"""

from __future__ import annotations

import json
import logging

import redis.asyncio as redis

from app.config import settings

logger = logging.getLogger(__name__)

_client: redis.Redis | None = None
_pool: redis.ConnectionPool | None = None


async def init_redis() -> None:
    global _client, _pool
    _pool = redis.ConnectionPool.from_url(settings.redis_url, decode_responses=True)
    _client = redis.Redis(connection_pool=_pool)
    try:
        await _client.ping()
        logger.info("Redis connected: %s", settings.redis_url)
    except redis.RedisError:
        logger.warning("Redis not reachable at %s — reads will return empty", settings.redis_url)
        _client = None


async def close_redis() -> None:
    global _client, _pool
    if _client:
        await _client.aclose()
    if _pool:
        await _pool.aclose()
    _client = None
    _pool = None


def get_client() -> redis.Redis | None:
    return _client


def _session_channel(session_id: str) -> str:
    return f"session:{session_id}:events"


def _stream_chunks(stream_id: str) -> str:
    return f"stream:{stream_id}:chunks"


def _stream_status(stream_id: str) -> str:
    return f"stream:{stream_id}:status"


def _session_status(session_id: str) -> str:
    return f"session:{session_id}:status"


def _session_latest_stream(session_id: str) -> str:
    return f"session:{session_id}:latest_stream"


async def subscribe(session_id: str):
    if not _client:
        return None
    pubsub = _client.pubsub()
    await pubsub.subscribe(_session_channel(session_id))
    return pubsub


async def get_stream_chunks(stream_id: str) -> list[dict]:
    if not _client:
        return []
    try:
        raw = await _client.lrange(_stream_chunks(stream_id), 0, -1)
        return [json.loads(r) for r in raw]
    except redis.RedisError:
        logger.warning("get_stream_chunks failed for stream %s", stream_id, exc_info=True)
        return []


async def get_stream_status(stream_id: str) -> str | None:
    if not _client:
        return None
    try:
        return await _client.get(_stream_status(stream_id))
    except redis.RedisError:
        return None


async def get_session_status(session_id: str) -> str:
    if not _client:
        return "offline"
    try:
        return (await _client.get(_session_status(session_id))) or "idle"
    except redis.RedisError:
        return "offline"


async def get_sessions_status(session_ids: list[str]) -> dict[str, str]:
    if not _client or not session_ids:
        return {sid: "offline" for sid in session_ids}
    try:
        values = await _client.mget([_session_status(sid) for sid in session_ids])
        return {sid: (val or "idle") for sid, val in zip(session_ids, values, strict=True)}
    except redis.RedisError:
        return {sid: "offline" for sid in session_ids}


async def get_latest_stream_id(session_id: str) -> str | None:
    """Return the most recent stream_id the supervisor attached to this session,
    used so a reconnecting WS client can replay any in-flight or just-finished
    stream."""
    if not _client:
        return None
    try:
        return await _client.get(_session_latest_stream(session_id))
    except redis.RedisError:
        return None


async def delete_all_session_keys(session_id: str) -> None:
    """Best-effort cleanup on session delete."""
    if not _client:
        return
    try:
        latest = await _client.get(_session_latest_stream(session_id))
        keys = [
            _session_channel(session_id),
            _session_status(session_id),
            _session_latest_stream(session_id),
        ]
        if latest:
            keys.extend([_stream_chunks(latest), _stream_status(latest)])
        await _client.delete(*keys)
    except redis.RedisError:
        logger.warning("delete_all_session_keys failed for %s", session_id, exc_info=True)
