"""DockerBackend — manages agent runtime containers via Docker API.

Multi-replica model:
  - Each agent has N replicas: aviary-agent-{agent_id}-{0..N-1}
  - All replicas share the same AGENTS_WORKSPACE_VOLUME subpath ({agent_id}),
    so conversation history (/workspace/.claude/{session_id}/) is visible to
    any replica — enables scale-out and replica substitution.
  - SESSIONS_WORKSPACE_VOLUME is mounted whole on every replica for A2A.

Load balancing: consistent hash session_id → replica (with capacity-aware
fallback). Scaling: supervisor scaling loop adjusts replica count based on
sessions_per_replica thresholds vs policy min/max.

Requires Docker Engine 25.0+ for volume subpath mounts.
"""

from __future__ import annotations

import asyncio
import hashlib
import logging
import time
from typing import AsyncIterator

import docker
import docker.errors
import httpx
from docker.models.containers import Container
from docker.types import Mount

from aviary_shared.naming import (
    AGENTS_WORKSPACE_VOLUME,
    LABEL_AGENT_ID,
    LABEL_MANAGED,
    LABEL_REPLICA,
    LABEL_ROLE,
    ROLE_AGENT_RUNTIME,
    RUNTIME_PORT,
    SESSIONS_WORKSPACE_VOLUME,
    agent_subpath,
    container_name,
)

from app.backends.protocol import TaskInfo, TaskMetrics

logger = logging.getLogger(__name__)

_INIT_IMAGE = "busybox:1.36"
_RUNTIME_UID = 1000
_RUNTIME_GID = 1000


class DockerBackend:
    def __init__(
        self,
        socket: str = "/var/run/docker.sock",
        primary_network: str = "aviary-agent-default",
    ) -> None:
        self._client = docker.DockerClient(base_url=f"unix://{socket}")
        self._primary_network = primary_network
        self._http = httpx.AsyncClient(timeout=30)
        self._require_network(self._primary_network)
        logger.info("DockerBackend ready: primary=%s", self._primary_network)

    def _require_network(self, name: str) -> None:
        try:
            self._client.networks.get(name)
        except docker.errors.NotFound as e:
            raise RuntimeError(
                f"Required Docker network {name!r} does not exist. "
                "Infrastructure networks must be pre-provisioned."
            ) from e

    # ── volumes & subpath init ────────────────────────────────────
    _chowned: set[str] = set()

    def _ensure_volume(self, name: str, chown_root: bool = False) -> None:
        try:
            self._client.volumes.get(name)
        except docker.errors.NotFound:
            self._client.volumes.create(name)
            logger.info("Created volume %s", name)

        if chown_root and name not in self._chowned:
            # Make the volume root writable by the runtime uid so it can
            # mkdir session subdirs. Run once per supervisor process.
            self._client.containers.run(
                _INIT_IMAGE,
                command=["sh", "-c", f"chown {_RUNTIME_UID}:{_RUNTIME_GID} /mnt"],
                volumes={name: {"bind": "/mnt", "mode": "rw"}},
                remove=True,
                detach=False,
            )
            self._chowned.add(name)

    def _ensure_agent_subpath(self, agent_id: str) -> None:
        sub = agent_subpath(agent_id)
        cmd = f"mkdir -p /mnt/{sub} && chown {_RUNTIME_UID}:{_RUNTIME_GID} /mnt/{sub}"
        self._client.containers.run(
            _INIT_IMAGE,
            command=["sh", "-c", cmd],
            volumes={AGENTS_WORKSPACE_VOLUME: {"bind": "/mnt", "mode": "rw"}},
            remove=True,
            detach=False,
        )

    # ── container helpers ─────────────────────────────────────────
    def _labels(self, agent_id: str, replica: int) -> dict[str, str]:
        return {
            LABEL_AGENT_ID: agent_id,
            LABEL_REPLICA: str(replica),
            LABEL_MANAGED: "true",
            LABEL_ROLE: ROLE_AGENT_RUNTIME,
        }

    def _container_url(self, host: str) -> str:
        return f"http://{host}:{RUNTIME_PORT}"

    def _build_mounts(self, agent_id: str) -> list[Mount]:
        agent_mount = Mount(
            target="/workspace",
            source=AGENTS_WORKSPACE_VOLUME,
            type="volume",
            read_only=False,
            no_copy=True,  # don't clobber supervisor-prepared subpath ownership
        )
        # docker-py 7.x does not expose `subpath` directly; inject into the
        # raw Docker API payload. VolumeOptions.Subpath requires Docker 25.0+.
        agent_mount.setdefault("VolumeOptions", {})["Subpath"] = agent_subpath(agent_id)

        return [
            agent_mount,
            Mount(
                target="/workspace-shared",
                source=SESSIONS_WORKSPACE_VOLUME,
                type="volume",
                read_only=False,
                no_copy=True,
            ),
        ]

    def _to_task_info(self, container: Container, agent_id: str, replica: int) -> TaskInfo:
        return TaskInfo(
            task_id=container.id or "",
            agent_id=agent_id,
            replica=replica,
            host=container.name or container_name(agent_id, replica),
            port=RUNTIME_PORT,
            status=container.status,
            created_at=time.time(),
        )

    def _list_replica_containers(self, agent_id: str) -> list[tuple[Container, int]]:
        containers = self._client.containers.list(
            all=True,
            filters={"label": [f"{LABEL_AGENT_ID}={agent_id}", f"{LABEL_MANAGED}=true"]},
        )
        result: list[tuple[Container, int]] = []
        for c in containers:
            replica_str = c.labels.get(LABEL_REPLICA, "0")
            try:
                replica = int(replica_str)
            except ValueError:
                continue
            result.append((c, replica))
        result.sort(key=lambda x: x[1])
        return result

    def _next_replica_idx(self, agent_id: str) -> int:
        existing = self._list_replica_containers(agent_id)
        if not existing:
            return 0
        used = {idx for _, idx in existing}
        idx = 0
        while idx in used:
            idx += 1
        return idx

    # ── storage ───────────────────────────────────────────────────
    async def ensure_storage(self, agent_id: str) -> dict[str, str]:
        self._ensure_volume(AGENTS_WORKSPACE_VOLUME)
        self._ensure_volume(SESSIONS_WORKSPACE_VOLUME, chown_root=True)
        self._ensure_agent_subpath(agent_id)
        return {
            "agents_workspace": AGENTS_WORKSPACE_VOLUME,
            "agent_subpath": agent_subpath(agent_id),
            "sessions_workspace": SESSIONS_WORKSPACE_VOLUME,
        }

    # ── replica lifecycle ─────────────────────────────────────────
    async def list_replicas(self, agent_id: str) -> list[TaskInfo]:
        return [
            self._to_task_info(c, agent_id, idx)
            for c, idx in self._list_replica_containers(agent_id)
        ]

    async def create_replica(
        self,
        agent_id: str,
        image: str,
        env: dict[str, str],
        network_policy: dict | None = None,
        resource_limits: dict | None = None,
    ) -> TaskInfo:
        self._ensure_volume(AGENTS_WORKSPACE_VOLUME)
        self._ensure_volume(SESSIONS_WORKSPACE_VOLUME, chown_root=True)
        self._ensure_agent_subpath(agent_id)

        replica = self._next_replica_idx(agent_id)
        name = container_name(agent_id, replica)
        # Docker only consumes extra_networks; subnets is Fargate-only.
        extras = (network_policy or {}).get("extra_networks") or []
        for net in extras:
            self._require_network(net)

        container: Container = self._client.containers.run(  # type: ignore[assignment]
            image,
            detach=True,
            name=name,
            environment=env,
            labels=self._labels(agent_id, replica),
            mounts=self._build_mounts(agent_id),
            network=self._primary_network,
            restart_policy={"Name": "unless-stopped"},  # type: ignore[arg-type]
            # bubblewrap needs unprivileged user namespaces; the default
            # Docker seccomp profile blocks the required syscalls.
            security_opt=["seccomp=unconfined"],
        )

        for net in extras:
            self._client.networks.get(net).connect(container)

        logger.info(
            "Created replica %s for agent %s (networks=%s)",
            name, agent_id, [self._primary_network, *extras],
        )
        return self._to_task_info(container, agent_id, replica)

    async def stop_replica(self, task: TaskInfo) -> bool:
        try:
            c = self._client.containers.get(task.task_id)
        except docker.errors.NotFound:
            return False
        try:
            c.stop(timeout=30)
            c.remove(force=True)
            logger.info("Stopped replica %s (agent %s)", c.name, task.agent_id)
            return True
        except docker.errors.APIError as e:
            logger.warning("Failed to stop replica %s: %s", task.task_id, e)
            return False

    async def scale_to(
        self,
        agent_id: str,
        target: int,
        image: str,
        env: dict[str, str],
        network_policy: dict | None = None,
    ) -> int:
        replicas = await self.list_replicas(agent_id)
        current = len(replicas)

        if target > current:
            for _ in range(target - current):
                await self.create_replica(
                    agent_id, image, env, network_policy=network_policy,
                )
        elif target < current:
            # Prefer removing replicas with fewest active sessions (least disruptive).
            scored: list[tuple[int, TaskInfo]] = []
            for r in replicas:
                m = await self.get_replica_metrics(r)
                load = m.sessions_active if m else 0
                scored.append((load, r))
            scored.sort(key=lambda x: x[0])
            to_remove = current - target
            for _, r in scored[:to_remove]:
                await self.stop_replica(r)

        return len(await self.list_replicas(agent_id))

    async def stop_all_replicas(self, agent_id: str) -> int:
        replicas = await self.list_replicas(agent_id)
        count = 0
        for r in replicas:
            if await self.stop_replica(r):
                count += 1
        return count

    # ── readiness & metrics ───────────────────────────────────────
    async def wait_for_ready(self, task: TaskInfo, timeout: int = 90) -> bool:
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            try:
                resp = await self._http.get(
                    f"{self._container_url(task.host)}/ready",
                    timeout=5,
                )
                if resp.status_code == 200:
                    return True
            except Exception:
                pass
            await asyncio.sleep(1)
        return False

    async def get_replica_metrics(self, task: TaskInfo) -> TaskMetrics | None:
        if task.status != "running":
            return None
        try:
            resp = await self._http.get(f"{self._container_url(task.host)}/metrics")
            data = resp.json()
            return TaskMetrics(
                sessions_active=data.get("sessions_active", 0),
                sessions_streaming=data.get("sessions_streaming", 0),
                sessions_max=data.get("sessions_max", 0),
            )
        except Exception as e:
            logger.warning("Metrics fetch failed for replica %s: %s", task.host, e)
            return None

    # ── load balancing ────────────────────────────────────────────
    @staticmethod
    def _stable_hash(key: str) -> int:
        return int.from_bytes(hashlib.sha256(key.encode()).digest()[:8], "big")

    async def pick_replica(
        self, agent_id: str, session_id: str,
    ) -> TaskInfo | None:
        replicas = [
            r for r in await self.list_replicas(agent_id) if r.status == "running"
        ]
        if not replicas:
            return None

        # Consistent-ish hash: anchor by session_id, walk the ring looking for
        # capacity. Small replica counts make full consistent hashing overkill;
        # the walk still keeps the assignment stable under scaling when the
        # preferred replica is present.
        anchor = self._stable_hash(session_id) % len(replicas)
        for offset in range(len(replicas)):
            candidate = replicas[(anchor + offset) % len(replicas)]
            m = await self.get_replica_metrics(candidate)
            if m is None:
                continue
            if m.sessions_active < m.sessions_max:
                return candidate

        # All at capacity — still return the primary so caller can propagate 503.
        return replicas[anchor]

    # ── session operations ────────────────────────────────────────
    async def stream_message(
        self, agent_id: str, session_id: str, body: dict,
    ) -> AsyncIterator[bytes]:
        replica = await self.pick_replica(agent_id, session_id)
        if replica is None:
            raise RuntimeError(f"No running replica for agent {agent_id}")

        url = f"{self._container_url(replica.host)}/message"
        async with self._http.stream("POST", url, json=body, timeout=None) as resp:
            resp.raise_for_status()
            async for chunk in resp.aiter_bytes():
                yield chunk

    async def abort_session(self, agent_id: str, session_id: str) -> bool:
        replica = await self.pick_replica(agent_id, session_id)
        if replica is None:
            return False
        try:
            resp = await self._http.post(
                f"{self._container_url(replica.host)}/abort/{session_id}",
                timeout=5,
            )
            return resp.status_code == 200
        except Exception:
            return False

    async def cleanup_session(self, agent_id: str, session_id: str) -> dict:
        # Cleanup must hit every replica — shared /workspace-shared is visible
        # to all, and any replica might have created a .claude/{sid}/ subdir.
        replicas = await self.list_replicas(agent_id)
        if not replicas:
            return {"status": "no_replicas"}

        results: list[dict] = []
        for r in replicas:
            try:
                resp = await self._http.delete(
                    f"{self._container_url(r.host)}/sessions/{session_id}/workspace",
                    timeout=5,
                )
                results.append({"replica": r.replica, **resp.json()})
            except Exception as e:
                results.append({"replica": r.replica, "status": "error", "detail": str(e)})
        return {"results": results}

    # ── backend health ────────────────────────────────────────────
    async def health_check(self) -> dict:
        try:
            self._client.ping()
            return {"status": "ok", "backend": "docker"}
        except Exception as e:
            return {"status": "error", "detail": str(e)}
