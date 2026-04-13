"""Centralized naming conventions for Aviary resources (Docker/Fargate).

Shared-volume model (mirrors Fargate + EFS with Access Points):
  - AGENTS_WORKSPACE_VOLUME: one volume, per-agent subpath → /workspace.
    All replicas of the same agent share this subpath, so any replica can
    serve any session (conversation history lives on shared storage).
  - SESSIONS_WORKSPACE_VOLUME: one volume, mounted whole at /workspace-shared;
    bwrap overlays in the runtime expose only the current session's subdir
    (enables A2A file sharing across agents within the same session).
"""

RUNTIME_PORT = 3000

AGENTS_WORKSPACE_VOLUME = "aviary-agents-workspace"
SESSIONS_WORKSPACE_VOLUME = "aviary-sessions-workspace"

# Docker labels
LABEL_AGENT_ID = "aviary.agent-id"
LABEL_REPLICA = "aviary.replica"
LABEL_MANAGED = "aviary.managed"
LABEL_ROLE = "aviary.role"
ROLE_AGENT_RUNTIME = "agent-runtime"


def container_name(agent_id: str, replica: int) -> str:
    return f"aviary-agent-{agent_id}-{replica}"


def agent_subpath(agent_id: str) -> str:
    """Subpath inside AGENTS_WORKSPACE_VOLUME. Shared across all replicas of an agent."""
    return agent_id
