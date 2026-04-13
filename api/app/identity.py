"""Process-identity helper — backend-agnostic stable ID for this API instance.

Docker:         HOSTNAME is the short container id.
ECS/Fargate:    HOSTNAME is the task's internal DNS (e.g. ip-10-0-1-5.ec2.internal).
Bare metal:     falls back to socket.gethostname().

A random suffix per process boot guarantees that the same host running a
restarted process never collides with its own stale locks/leases.
"""

import os
import socket
import uuid
from functools import lru_cache


@lru_cache(maxsize=1)
def instance_id() -> str:
    host = os.environ.get("HOSTNAME") or socket.gethostname() or "unknown"
    return f"{host}-{uuid.uuid4().hex[:8]}"
