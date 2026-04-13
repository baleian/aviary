"""Redis Pub/Sub and Streams utilities for event publishing."""

from __future__ import annotations

import json

import redis.asyncio as aioredis


class RedisPublisher:
    def __init__(self, url: str) -> None:
        self._redis = aioredis.from_url(url, decode_responses=True)

    async def close(self) -> None:
        await self._redis.aclose()

    async def publish_stream_event(self, session_id: str, event_data: dict) -> None:
        channel = f"session:{session_id}:stream"
        await self._redis.publish(channel, json.dumps(event_data))

    async def publish_durable_event(
        self, session_id: str, event_type: str, data: dict,
    ) -> None:
        stream_key = f"session:{session_id}:events"
        await self._redis.xadd(
            stream_key,
            {"type": event_type, "data": json.dumps(data)},
        )

    @property
    def client(self) -> aioredis.Redis:
        return self._redis
