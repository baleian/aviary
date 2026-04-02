"""Redis service for admin — egress policy sync."""

import json
import logging

import redis.asyncio as aioredis

from app.config import settings

logger = logging.getLogger(__name__)

_redis: aioredis.Redis | None = None


async def init_redis() -> None:
    global _redis
    _redis = aioredis.from_url(settings.redis_url, decode_responses=True)
    logger.info("Redis connected → %s", settings.redis_url)


async def close_redis() -> None:
    global _redis
    if _redis:
        await _redis.aclose()
        _redis = None


def get_client() -> aioredis.Redis | None:
    return _redis


async def sync_egress_policy(agent_id: str, policy: dict) -> None:
    """Write agent egress policy to Redis for the egress-proxy."""
    if not _redis:
        return
    key = f"egress:{agent_id}"
    await _redis.set(key, json.dumps(policy))


async def delete_egress_policy(agent_id: str) -> None:
    """Remove agent egress policy from Redis."""
    if not _redis:
        return
    await _redis.delete(f"egress:{agent_id}")
