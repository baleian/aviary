"""Public Agent Catalog — browse, detail, versions, facets."""

from __future__ import annotations

import uuid

from fastapi import APIRouter, Depends, Query
from sqlalchemy.ext.asyncio import AsyncSession

from app.auth.dependencies import get_current_user
from app.db.models import User
from app.db.session import get_db
from app.schemas.catalog import (
    AgentVersionListResponse,
    AgentVersionResponse,
    AgentVersionSummary,
    CatalogAgentDetail,
    CatalogAgentSummary,
    CatalogCategory,
    CatalogFacetsResponse,
    CatalogListResponse,
)
from app.services import catalog_service

router = APIRouter()


@router.get("", response_model=CatalogListResponse)
async def browse_catalog(
    q: str | None = Query(None),
    category: list[CatalogCategory] | None = Query(None),
    mcp_server: list[str] | None = Query(None),
    sort: str = Query("recent"),
    offset: int = Query(0, ge=0),
    limit: int = Query(50, ge=1, le=100),
    user: User = Depends(get_current_user),
    db: AsyncSession = Depends(get_db),
):
    cats = [c.value for c in category] if category else None
    items, total = await catalog_service.list_catalog(
        db,
        q=q,
        categories=cats,
        mcp_servers=mcp_server,
        sort=sort,
        offset=offset,
        limit=limit,
    )
    return CatalogListResponse(
        items=[CatalogAgentSummary(**item) for item in items],
        total=total,
    )


@router.get("/facets", response_model=CatalogFacetsResponse)
async def catalog_facets(
    q: str | None = Query(None),
    category: list[CatalogCategory] | None = Query(None),
    user: User = Depends(get_current_user),
    db: AsyncSession = Depends(get_db),
):
    cats = [c.value for c in category] if category else None
    data = await catalog_service.compute_facets(db, q=q, categories=cats)
    return CatalogFacetsResponse(**data)


@router.get("/{catalog_agent_id}", response_model=CatalogAgentDetail)
async def get_catalog_detail(
    catalog_agent_id: uuid.UUID,
    v: uuid.UUID | None = Query(None, description="Specific version id"),
    user: User = Depends(get_current_user),
    db: AsyncSession = Depends(get_db),
):
    detail = await catalog_service.get_catalog_detail(
        db, catalog_agent_id, user, version_id=v
    )
    detail["version"] = AgentVersionResponse.model_validate(detail["version"])
    return CatalogAgentDetail(**detail)


@router.get(
    "/{catalog_agent_id}/versions", response_model=AgentVersionListResponse
)
async def list_versions(
    catalog_agent_id: uuid.UUID,
    user: User = Depends(get_current_user),
    db: AsyncSession = Depends(get_db),
):
    versions, total = await catalog_service.list_versions(
        db, catalog_agent_id, include_unpublished=False
    )
    return AgentVersionListResponse(
        items=[AgentVersionSummary.model_validate(v) for v in versions],
        total=total,
    )
