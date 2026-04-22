"""Catalog publisher-side routes — /catalog/mine, rollback, unpublish,
version-level unpublish, version history."""

from __future__ import annotations

import uuid

from fastapi import APIRouter, Depends, Query, status
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.auth.dependencies import get_current_user
from app.db.models import AgentVersion, CatalogAgent, User
from app.db.session import get_db
from app.errors import NotFoundError, UnauthorizedError
from app.schemas.catalog import (
    AgentVersionListResponse,
    AgentVersionResponse,
    AgentVersionSummary,
    MyCatalogAgent,
    MyCatalogListResponse,
    RollbackRequest,
)
from app.services import catalog_service, publish_service

router = APIRouter()


async def _require_catalog_owner(
    catalog_agent_id: uuid.UUID,
    user: User = Depends(get_current_user),
    db: AsyncSession = Depends(get_db),
) -> CatalogAgent:
    ca = (await db.execute(
        select(CatalogAgent).where(CatalogAgent.id == catalog_agent_id)
    )).scalar_one_or_none()
    if ca is None:
        raise NotFoundError("Catalog agent not found")
    if ca.owner_id != user.id:
        raise UnauthorizedError("Not the owner of this catalog agent")
    return ca


@router.get("/mine", response_model=MyCatalogListResponse)
async def list_my_catalog(
    offset: int = Query(0, ge=0),
    limit: int = Query(50, ge=1, le=100),
    user: User = Depends(get_current_user),
    db: AsyncSession = Depends(get_db),
):
    items, total = await catalog_service.list_my_catalog(db, user, offset, limit)
    return MyCatalogListResponse(
        items=[MyCatalogAgent(**item) for item in items], total=total,
    )


async def _respond_mine(
    db: AsyncSession, user: User, ca: CatalogAgent
) -> MyCatalogAgent:
    item = await catalog_service.get_my_catalog_item(db, user, ca.id)
    if item is None:
        raise NotFoundError("Catalog agent not found")
    return MyCatalogAgent(**item)


@router.post(
    "/agents/{catalog_agent_id}/rollback", response_model=MyCatalogAgent
)
async def rollback(
    body: RollbackRequest,
    ca: CatalogAgent = Depends(_require_catalog_owner),
    db: AsyncSession = Depends(get_db),
    user: User = Depends(get_current_user),
):
    await publish_service.rollback_catalog_agent(db, ca, uuid.UUID(body.version_id))
    return await _respond_mine(db, user, ca)


@router.post(
    "/agents/{catalog_agent_id}/unpublish", response_model=MyCatalogAgent
)
async def unpublish(
    ca: CatalogAgent = Depends(_require_catalog_owner),
    db: AsyncSession = Depends(get_db),
    user: User = Depends(get_current_user),
):
    await publish_service.unpublish_catalog_agent(db, ca)
    return await _respond_mine(db, user, ca)


@router.post(
    "/agents/{catalog_agent_id}/republish", response_model=MyCatalogAgent
)
async def republish(
    ca: CatalogAgent = Depends(_require_catalog_owner),
    db: AsyncSession = Depends(get_db),
    user: User = Depends(get_current_user),
):
    await publish_service.republish_catalog_agent(db, ca)
    return await _respond_mine(db, user, ca)


@router.post(
    "/versions/{version_id}/unpublish", response_model=AgentVersionResponse
)
async def unpublish_version(
    version_id: uuid.UUID,
    user: User = Depends(get_current_user),
    db: AsyncSession = Depends(get_db),
):
    version = (await db.execute(
        select(AgentVersion).where(AgentVersion.id == version_id)
    )).scalar_one_or_none()
    if version is None:
        raise NotFoundError("Version not found")
    ca = (await db.execute(
        select(CatalogAgent).where(CatalogAgent.id == version.catalog_agent_id)
    )).scalar_one()
    if ca.owner_id != user.id:
        raise UnauthorizedError("Not the owner of this catalog agent")
    updated = await publish_service.unpublish_version(db, version)
    return AgentVersionResponse.model_validate(updated)


@router.get(
    "/agents/{catalog_agent_id}/versions", response_model=AgentVersionListResponse
)
async def list_my_versions(
    ca: CatalogAgent = Depends(_require_catalog_owner),
    db: AsyncSession = Depends(get_db),
):
    versions, total = await catalog_service.list_versions(
        db, ca.id, include_unpublished=True
    )
    return AgentVersionListResponse(
        items=[AgentVersionSummary.model_validate(v) for v in versions],
        total=total,
    )


@router.delete(
    "/agents/{catalog_agent_id}", status_code=status.HTTP_204_NO_CONTENT
)
async def delete_catalog_agent(
    ca: CatalogAgent = Depends(_require_catalog_owner),
    db: AsyncSession = Depends(get_db),
):
    from app.db.models import Agent, CatalogImport
    from app.errors import ConflictError

    has_imports = (await db.execute(
        select(CatalogImport.id).where(
            CatalogImport.catalog_agent_id == ca.id
        ).limit(1)
    )).scalar_one_or_none()
    if has_imports:
        raise ConflictError(
            "Catalog agent has active imports. Unpublish first to stop new imports."
        )

    linked = (await db.execute(
        select(Agent).where(Agent.linked_catalog_agent_id == ca.id)
    )).scalars().all()
    for a in linked:
        a.linked_catalog_agent_id = None

    await db.delete(ca)
    await db.flush()
    return None
