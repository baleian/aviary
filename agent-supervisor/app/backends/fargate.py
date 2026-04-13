"""FargateBackend — AWS ECS Fargate runtime backend (stub).

Production architecture:
  - ECS tasks (one task per replica) sharing an EFS access point per agent
  - EFS mount target = AGENTS_WORKSPACE_VOLUME equivalent
  - Security Groups for network isolation
  - Cloud Map (service discovery) for replica DNS names
  - CloudWatch for metrics/logging
"""

from __future__ import annotations

from typing import AsyncIterator

from app.backends.protocol import TaskInfo, TaskMetrics


class FargateBackend:

    def __init__(self, cluster: str, subnets: list[str], security_groups: list[str]) -> None:
        raise NotImplementedError("FargateBackend is a Phase 2 deliverable")

    async def ensure_storage(self, agent_id: str) -> dict[str, str]:
        raise NotImplementedError

    async def list_replicas(self, agent_id: str) -> list[TaskInfo]:
        raise NotImplementedError

    async def create_replica(
        self, agent_id: str, image: str, env: dict[str, str],
        network_policy: dict | None = None,
        resource_limits: dict | None = None,
    ) -> TaskInfo:
        # network_policy: {"extra_networks": [sg_ids], "subnets": [subnet_ids]}
        # maps to awsvpcConfiguration.securityGroups / .subnets respectively.
        raise NotImplementedError

    async def stop_replica(self, task: TaskInfo) -> bool:
        raise NotImplementedError

    async def scale_to(
        self, agent_id: str, target: int, image: str, env: dict[str, str],
        network_policy: dict | None = None,
    ) -> int:
        raise NotImplementedError

    async def stop_all_replicas(self, agent_id: str) -> int:
        raise NotImplementedError

    async def purge_agent_storage(self, agent_id: str) -> None:
        raise NotImplementedError

    async def wait_for_ready(self, task: TaskInfo, timeout: int = 90) -> bool:
        raise NotImplementedError

    async def get_replica_metrics(self, task: TaskInfo) -> TaskMetrics | None:
        raise NotImplementedError

    async def pick_replica(self, agent_id: str, session_id: str) -> TaskInfo | None:
        raise NotImplementedError

    async def stream_message(
        self, agent_id: str, session_id: str, body: dict,
    ) -> AsyncIterator[bytes]:
        raise NotImplementedError
        yield  # type: ignore[unreachable]

    async def abort_session(self, agent_id: str, session_id: str) -> bool:
        raise NotImplementedError

    async def cleanup_session(self, agent_id: str, session_id: str) -> dict:
        raise NotImplementedError

    async def health_check(self) -> dict:
        raise NotImplementedError
