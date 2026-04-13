from __future__ import annotations

from dataclasses import dataclass
from typing import AsyncIterator, Protocol, runtime_checkable


@dataclass
class TaskInfo:
    task_id: str
    agent_id: str
    replica: int
    host: str
    port: int
    status: str
    created_at: float


@dataclass
class TaskMetrics:
    sessions_active: int
    sessions_streaming: int
    sessions_max: int


@runtime_checkable
class RuntimeBackend(Protocol):
    # ── storage ────────────────────────────────────────────────
    async def ensure_storage(self, agent_id: str) -> dict[str, str]: ...

    # ── replica lifecycle ──────────────────────────────────────
    async def list_replicas(self, agent_id: str) -> list[TaskInfo]: ...

    async def create_replica(
        self,
        agent_id: str,
        image: str,
        env: dict[str, str],
        network_policy: dict | None = None,
        resource_limits: dict | None = None,
    ) -> TaskInfo: ...

    async def stop_replica(self, task: TaskInfo) -> bool: ...

    async def scale_to(
        self,
        agent_id: str,
        target: int,
        image: str,
        env: dict[str, str],
        network_policy: dict | None = None,
    ) -> int:
        """Scale agent replicas to `target` count. Returns actual replica count after."""
        ...

    async def stop_all_replicas(self, agent_id: str) -> int: ...

    # ── replica readiness & metrics ────────────────────────────
    async def wait_for_ready(self, task: TaskInfo, timeout: int = 90) -> bool: ...

    async def get_replica_metrics(self, task: TaskInfo) -> TaskMetrics | None: ...

    # ── load balancing ─────────────────────────────────────────
    async def pick_replica(
        self, agent_id: str, session_id: str,
    ) -> TaskInfo | None:
        """Consistent hash routing: session_id → replica. Prefers replicas with capacity."""
        ...

    # ── session operations (auto-pick replica) ─────────────────
    async def stream_message(
        self, agent_id: str, session_id: str, body: dict,
    ) -> AsyncIterator[bytes]: ...

    async def abort_session(self, agent_id: str, session_id: str) -> bool: ...

    async def cleanup_session(self, agent_id: str, session_id: str) -> dict: ...

    # ── backend health ─────────────────────────────────────────
    async def health_check(self) -> dict: ...
