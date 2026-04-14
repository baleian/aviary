"""Supervisor HTTP client used by the admin console."""

from __future__ import annotations

import httpx

from app.config import settings


class SupervisorClient:
    def __init__(self, base_url: str) -> None:
        self._base = base_url.rstrip("/")
        self._client = httpx.AsyncClient(base_url=self._base, timeout=30.0)

    async def close(self) -> None:
        await self._client.aclose()

    async def register(self, agent_id: str) -> dict:
        r = await self._client.post(f"/v1/agents/{agent_id}/register")
        r.raise_for_status()
        return r.json()

    async def run(self, agent_id: str) -> dict:
        r = await self._client.post(f"/v1/agents/{agent_id}/run")
        r.raise_for_status()
        return r.json()

    async def restart(self, agent_id: str) -> dict:
        r = await self._client.post(f"/v1/agents/{agent_id}/restart")
        r.raise_for_status()
        return r.json()

    async def scale(self, agent_id: str, target: int) -> dict:
        r = await self._client.post(f"/v1/agents/{agent_id}/scale", params={"target": target})
        r.raise_for_status()
        return r.json()

    async def deactivate(self, agent_id: str) -> dict:
        """Stop all tasks, keep the workspace volume for later restore."""
        r = await self._client.delete(f"/v1/agents/{agent_id}")
        r.raise_for_status()
        return r.json()

    async def purge(self, agent_id: str) -> dict:
        """Stop all tasks AND delete the agent's workspace subpath."""
        r = await self._client.delete(f"/v1/agents/{agent_id}", params={"purge": "true"})
        r.raise_for_status()
        return r.json()

    async def list_tasks(self, agent_id: str) -> list[dict]:
        r = await self._client.get(f"/v1/agents/{agent_id}/replicas")
        if r.status_code != 200:
            return []
        return r.json().get("replicas", [])

    async def metrics(self, agent_id: str) -> dict | None:
        r = await self._client.get(f"/v1/agents/{agent_id}/metrics")
        if r.status_code != 200:
            return None
        return r.json()


client = SupervisorClient(settings.supervisor_url)
