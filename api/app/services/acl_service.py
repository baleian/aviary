"""ACL permission resolution — thin wrapper over shared ACL module.

Keeps the same public API used by the API server (User/Agent ORM objects),
but delegates logic to aviary_shared.auth.acl.
"""

from sqlalchemy.ext.asyncio import AsyncSession

from app.db.models import Agent, User, Workflow
from aviary_shared.auth.acl import (  # noqa: F401
    ROLE_HIERARCHY,
    ROLE_PERMISSIONS,
    WORKFLOW_ROLE_PERMISSIONS,
    has_permission,
    has_workflow_permission,
)
from aviary_shared.auth.acl import resolve_agent_role as _resolve_agent_role
from aviary_shared.auth.acl import resolve_workflow_role as _resolve_workflow_role


async def resolve_agent_role(
    db: AsyncSession, user: User, agent: Agent
) -> str | None:
    """Resolve the effective role a user has on an agent. Returns None if no access."""
    return await _resolve_agent_role(db, user.id, agent)


async def check_agent_permission(
    db: AsyncSession, user: User, agent: Agent, permission: str
) -> None:
    """Raise PermissionError if user lacks the required permission on the agent."""
    role = await resolve_agent_role(db, user, agent)
    if not has_permission(role, permission):
        raise PermissionError(f"You do not have '{permission}' permission on this agent")


async def resolve_workflow_role(
    db: AsyncSession, user: User, workflow: Workflow
) -> str | None:
    return await _resolve_workflow_role(db, user.id, workflow)


async def check_workflow_permission(
    db: AsyncSession, user: User, workflow: Workflow, permission: str
) -> None:
    role = await resolve_workflow_role(db, user, workflow)
    if not has_workflow_permission(role, permission):
        raise PermissionError(f"You do not have '{permission}' permission on this workflow")
