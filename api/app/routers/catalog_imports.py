"""Consumer import / fork + owner restore routes."""

from __future__ import annotations

import uuid

from fastapi import APIRouter, Depends, Query, status
from sqlalchemy import func, select
from sqlalchemy.ext.asyncio import AsyncSession

from app.auth.dependencies import get_current_user, require_agent_owner
from app.db.models import Agent, CatalogAgent, CatalogImport, User
from app.db.session import get_db
from app.errors import ConflictError, NotFoundError, UnauthorizedError
from app.schemas.agent import AgentListResponse, AgentResponse
from app.schemas.catalog import ImportPatchRequest, ImportRequest
from app.services import agent_service, import_service

router = APIRouter()


@router.get("/imports", response_model=AgentListResponse)
async def list_my_imports(
    offset: int = Query(0, ge=0),
    limit: int = Query(100, ge=1, le=200),
    user: User = Depends(get_current_user),
    db: AsyncSession = Depends(get_db),
):
    base = select(Agent).where(
        Agent.owner_id == user.id,
        Agent.catalog_import_id.isnot(None),
    )
    total = (await db.execute(
        select(func.count()).select_from(base.subquery())
    )).scalar_one() or 0
    rows = (await db.execute(
        base.order_by(Agent.created_at.desc()).offset(offset).limit(limit)
    )).scalars().all()
    return AgentListResponse(
        items=await agent_service.build_agent_responses(db, list(rows)),
        total=int(total),
    )


@router.post("/imports", response_model=dict)
async def create_imports(
    body: ImportRequest,
    user: User = Depends(get_current_user),
    db: AsyncSession = Depends(get_db),
):
    created: list[AgentResponse] = []
    already: list[AgentResponse] = []
    for item in body.items:
        pinned = (
            uuid.UUID(item.pinned_version_id) if item.pinned_version_id else None
        )
        agent, was_created = await import_service.create_import(
            db, user, uuid.UUID(item.catalog_agent_id), pinned,
        )
        resp = await agent_service.build_agent_response(db, agent)
        (created if was_created else already).append(resp)
    return {
        "created": [r.model_dump(by_alias=True, mode="json") for r in created],
        "already_imported": [
            r.model_dump(by_alias=True, mode="json") for r in already
        ],
    }


@router.get("/imports/{agent_id}")
async def get_import(
    agent: Agent = Depends(require_agent_owner()),
    db: AsyncSession = Depends(get_db),
):
    # Deprecated — AgentResponse now carries pinned_version_id.
    if agent.catalog_import_id is None:
        raise ConflictError("Not an imported agent")
    ci = (await db.execute(
        select(CatalogImport).where(CatalogImport.id == agent.catalog_import_id)
    )).scalar_one()
    ca = (await db.execute(
        select(CatalogAgent).where(CatalogAgent.id == ci.catalog_agent_id)
    )).scalar_one()
    return {
        "agent_id": str(agent.id),
        "catalog_agent_id": str(ca.id),
        "pinned_version_id": str(ci.pinned_version_id) if ci.pinned_version_id else None,
        "effective_version_id": str(ci.pinned_version_id or ca.current_version_id)
            if (ci.pinned_version_id or ca.current_version_id) else None,
    }


@router.patch("/imports/{agent_id}", response_model=AgentResponse)
async def patch_import(
    body: ImportPatchRequest,
    agent: Agent = Depends(require_agent_owner()),
    db: AsyncSession = Depends(get_db),
):
    pinned = uuid.UUID(body.pinned_version_id) if body.pinned_version_id else None
    updated = await import_service.patch_import_pin(db, agent, pinned)
    return await agent_service.build_agent_response(db, updated)


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
    return await agent_service.build_agent_response(db, updated)


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
    return await agent_service.build_agent_response(db, agent)
