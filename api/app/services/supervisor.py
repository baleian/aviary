"""Thin typed client for agent-supervisor.

All HTTP traffic to the supervisor flows through this single module so we
never scatter raw httpx calls across routers/services.
"""

import logging
from collections.abc import AsyncIterator

import httpx

from app.config import settings

logger = logging.getLogger(__name__)


class SupervisorClient:
    def __init__(self, base_url: str):
        self._base = base_url.rstrip("/")
        self._client: httpx.AsyncClient | None = None

    async def start(self) -> None:
        self._client = httpx.AsyncClient(base_url=self._base, timeout=30)

    async def close(self) -> None:
        if self._client is not None:
            await self._client.aclose()
            self._client = None

    def _require(self) -> httpx.AsyncClient:
        if self._client is None:
            raise RuntimeError("SupervisorClient not started")
        return self._client

    async def health(self) -> bool:
        try:
            r = await self._require().get("/v1/health", timeout=5)
            return r.status_code == 200
        except httpx.HTTPError:
            return False

    async def register(self, agent_id: str) -> dict:
        r = await self._require().post(f"/v1/agents/{agent_id}/register")
        r.raise_for_status()
        return r.json()

    async def run(self, agent_id: str) -> dict:
        r = await self._require().post(f"/v1/agents/{agent_id}/run")
        r.raise_for_status()
        return r.json()

    async def delete(self, agent_id: str, *, purge: bool = False) -> None:
        params = {"purge": "true"} if purge else None
        r = await self._require().delete(f"/v1/agents/{agent_id}", params=params)
        r.raise_for_status()

    async def wait_ready(self, agent_id: str, timeout: int | None = None) -> bool:
        timeout = timeout or settings.agent_ready_timeout
        try:
            r = await self._require().get(
                f"/v1/agents/{agent_id}/wait",
                params={"timeout": timeout},
                timeout=timeout + 5,
            )
            return r.status_code == 200 and r.json().get("status") == "ready"
        except httpx.HTTPError:
            return False

    async def abort_session(self, agent_id: str, session_id: str) -> bool:
        try:
            r = await self._require().post(f"/v1/agents/{agent_id}/sessions/{session_id}/abort")
            return r.status_code == 200
        except httpx.HTTPError:
            return False

    async def cleanup_session(self, agent_id: str, session_id: str) -> None:
        try:
            await self._require().delete(f"/v1/agents/{agent_id}/sessions/{session_id}")
        except httpx.HTTPError as e:
            logger.warning("cleanup_session failed: %s", e)

    def stream_message(self, agent_id: str, session_id: str, body: dict) -> AsyncIterator[str]:
        """Return an async line iterator over the supervisor SSE response."""
        client = self._require()

        async def _iter() -> AsyncIterator[str]:
            async with client.stream(
                "POST",
                f"/v1/agents/{agent_id}/sessions/{session_id}/message",
                json=body,
                timeout=None,
            ) as resp:
                resp.raise_for_status()
                async for line in resp.aiter_lines():
                    if line.startswith("data: "):
                        yield line[6:]

        return _iter()


supervisor_client = SupervisorClient(settings.supervisor_url)
