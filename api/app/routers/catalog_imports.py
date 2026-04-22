"""Consumer import / fork + owner restore routes."""

from __future__ import annotations

import uuid

from fastapi import APIRouter, Depends, status
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.auth.dependencies import get_current_user, require_agent_owner
from app.db.models import Agent, CatalogAgent, User
from app.db.session import get_db
from app.errors import NotFoundError, UnauthorizedError
from app.schemas.agent import AgentResponse
from app.schemas.catalog import ImportPatchRequest, ImportRequest
from app.services import import_service

router = APIRouter()


@router.post("/imports", response_model=dict)
async def create_imports(
    body: ImportRequest,
    user: User = Depends(get_current_user),
    db: AsyncSession = Depends(get_db),
):
    """Bulk import catalog agents as new agents rows owned by the caller.

    Response: ``{"created": [AgentResponse, ...], "already_imported": [AgentResponse, ...]}``.
    """
    created: list[AgentResponse] = []
    already: list[AgentResponse] = []
    for item in body.items:
        pinned = (
            uuid.UUID(item.pinned_version_id) if item.pinned_version_id else None
        )
        agent, was_created = await import_service.create_import(
            db, user, uuid.UUID(item.catalog_agent_id), pinned,
        )
        resp = AgentResponse.model_validate(agent)
        if was_created:
            created.append(resp)
        else:
            already.append(resp)
    return {
        "created": [r.model_dump(by_alias=True, mode="json") for r in created],
        "already_imported": [
            r.model_dump(by_alias=True, mode="json") for r in already
        ],
    }


@router.patch("/imports/{agent_id}", response_model=AgentResponse)
async def patch_import(
    body: ImportPatchRequest,
    agent: Agent = Depends(require_agent_owner()),
    db: AsyncSession = Depends(get_db),
):
    pinned = uuid.UUID(body.pinned_version_id) if body.pinned_version_id else None
    updated = await import_service.patch_import_pin(db, agent, pinned)
    return AgentResponse.model_validate(updated)


@router.delete("/imports/{agent_id}", status_code=status.HTTP_204_NO_CONTENT)
async def delete_import(
    agent: Agent = Depends(require_agent_owner()),
    db: AsyncSession = Depends(get_db),
):
    await import_service.delete_import(db, agent)
    return None


@router.post("/imports/{agent_id}/fork", response_model=AgentResponse)
async def fork_import(
    agent: Agent = Depends(require_agent_owner()),
    db: AsyncSession = Depends(get_db),
):
    updated = await import_service.fork_import(db, agent)
    return AgentResponse.model_validate(updated)


@router.post("/agents/{catalog_agent_id}/restore", response_model=AgentResponse)
async def restore_working_copy(
    catalog_agent_id: uuid.UUID,
    user: User = Depends(get_current_user),
    db: AsyncSession = Depends(get_db),
):
    ca = (await db.execute(
        select(CatalogAgent).where(CatalogAgent.id == catalog_agent_id)
    )).scalar_one_or_none()
    if ca is None:
        raise NotFoundError("Catalog agent not found")
    if ca.owner_id != user.id:
        raise UnauthorizedError("Not the owner of this catalog agent")

    agent = await import_service.restore_working_copy(db, user, ca)
    return AgentResponse.model_validate(agent)
