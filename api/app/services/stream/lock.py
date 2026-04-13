"""Redis-backed exclusive lease for a single stream_id.

Used to coordinate "who is currently feeding this WebSocket" across API
replicas. The lease uses a fencing token (holder_id) so that a stale owner
cannot accidentally refresh or release a lock that has already been taken
over by a new holder.

Key layout:
    stream:{stream_id}:owner   — string, JSON {holder_id, acquired_at}

Lifecycle:
    acquire()     — SET NX PX; returns True only if we now hold the lease.
    refresh()     — CAS via Lua; extends TTL iff we still hold it.
    release()     — CAS via Lua; deletes the key iff we still hold it.
    owner_of()    — read current holder_id (for observability / diagnostics).

The heartbeat loop is owned by the caller (stream manager) so that
cancellation is coupled to the stream task's lifetime.
"""

import json
import time
from dataclasses import dataclass

import redis.asyncio as aioredis

from app.deps import get_redis


# CAS Lua scripts — atomic compare-holder-then-act.
_LUA_REFRESH = """
local v = redis.call('GET', KEYS[1])
if not v then return 0 end
local ok, decoded = pcall(cjson.decode, v)
if not ok or decoded.holder_id ~= ARGV[1] then return 0 end
redis.call('PEXPIRE', KEYS[1], ARGV[2])
return 1
"""

_LUA_RELEASE = """
local v = redis.call('GET', KEYS[1])
if not v then return 0 end
local ok, decoded = pcall(cjson.decode, v)
if not ok or decoded.holder_id ~= ARGV[1] then return 0 end
redis.call('DEL', KEYS[1])
return 1
"""


def _key(stream_id: str) -> str:
    return f"stream:{stream_id}:owner"


@dataclass
class LeaseInfo:
    holder_id: str
    acquired_at: float


class StreamLock:
    """Thin wrapper over a Redis connection; all state lives in Redis."""

    def __init__(self, redis: aioredis.Redis) -> None:
        self._redis = redis
        self._refresh = redis.register_script(_LUA_REFRESH)
        self._release = redis.register_script(_LUA_RELEASE)

    async def acquire(self, stream_id: str, holder_id: str, ttl_s: int) -> bool:
        payload = json.dumps({"holder_id": holder_id, "acquired_at": time.time()})
        return bool(await self._redis.set(_key(stream_id), payload, nx=True, ex=ttl_s))

    async def refresh(self, stream_id: str, holder_id: str, ttl_s: int) -> bool:
        result = await self._refresh(keys=[_key(stream_id)], args=[holder_id, ttl_s * 1000])
        return bool(result)

    async def release(self, stream_id: str, holder_id: str) -> bool:
        result = await self._release(keys=[_key(stream_id)], args=[holder_id])
        return bool(result)

    async def owner_of(self, stream_id: str) -> LeaseInfo | None:
        raw = await self._redis.get(_key(stream_id))
        if not raw:
            return None
        try:
            d = json.loads(raw)
            return LeaseInfo(holder_id=d["holder_id"], acquired_at=float(d["acquired_at"]))
        except (ValueError, KeyError, TypeError):
            return None


def get_lock() -> StreamLock:
    return StreamLock(get_redis())
