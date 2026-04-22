"""Catalog browse service — list, detail, versions, facets.

All responses hydrate display fields via JOIN with the effective AgentVersion
snapshot, never from the agents-table import cache.
"""

from __future__ import annotations

import uuid
from typing import Any

from sqlalchemy import and_, func, or_, select
from sqlalchemy.ext.asyncio import AsyncSession

from app.db.models import (
    Agent,
    AgentVersion,
    CatalogAgent,
    McpAgentToolBinding,
    User,
)
from app.errors import NotFoundError


# Valid sort keys — `recent` = most-recently-published, `popular` = most imports.
_VALID_SORTS = {"recent", "popular", "name"}


def _extract_server_names(mcp_tool_bindings: list[dict[str, Any]] | None) -> list[str]:
    if not mcp_tool_bindings:
        return []
    seen: list[str] = []
    for b in mcp_tool_bindings:
        name = b.get("server_name")
        if name and name not in seen:
            seen.append(name)
    return seen


def _live_join(stmt):
    """Join condition that restricts to visible catalog + live current version."""
    return stmt.join(
        AgentVersion, AgentVersion.id == CatalogAgent.current_version_id
    ).where(
        CatalogAgent.is_published.is_(True),
        CatalogAgent.unpublished_at.is_(None),
        AgentVersion.unpublished_at.is_(None),
    )


async def _import_count_subquery():
    """Subquery expression returning number of agents.rows that import each catalog."""
    from app.db.models import CatalogImport

    return (
        select(
            CatalogImport.catalog_agent_id.label("cid"),
            func.count(Agent.id).label("import_count"),
        )
        .select_from(CatalogImport)
        .join(Agent, Agent.catalog_import_id == CatalogImport.id)
        .group_by(CatalogImport.catalog_agent_id)
        .subquery()
    )


async def list_catalog(
    db: AsyncSession,
    *,
    q: str | None,
    categories: list[str] | None,
    mcp_servers: list[str] | None,
    sort: str,
    offset: int,
    limit: int,
) -> tuple[list[dict], int]:
    if sort not in _VALID_SORTS:
        sort = "recent"

    import_count_sq = await _import_count_subquery()

    stmt = (
        select(
            CatalogAgent,
            AgentVersion,
            User.email.label("owner_email"),
            func.coalesce(import_count_sq.c.import_count, 0).label("import_count"),
        )
        .select_from(CatalogAgent)
        .join(User, User.id == CatalogAgent.owner_id)
        .outerjoin(import_count_sq, import_count_sq.c.cid == CatalogAgent.id)
    )
    stmt = _live_join(stmt)

    if q:
        pattern = f"%{q}%"
        stmt = stmt.where(
            or_(AgentVersion.name.ilike(pattern), AgentVersion.description.ilike(pattern))
        )
    if categories:
        stmt = stmt.where(AgentVersion.category.in_(categories))
    if mcp_servers:
        # Match any version whose mcp_tool_bindings JSONB contains ANY of the
        # requested server names. JSONB @> with a single-element list works
        # per-server; OR them for multi.
        from sqlalchemy import cast
        from sqlalchemy.dialects.postgresql import JSONB

        conds = [
            AgentVersion.mcp_tool_bindings.op("@>")(
                cast([{"server_name": srv}], JSONB)
            )
            for srv in mcp_servers
        ]
        stmt = stmt.where(or_(*conds))

    total = (await db.execute(
        select(func.count()).select_from(stmt.subquery())
    )).scalar_one()

    if sort == "recent":
        stmt = stmt.order_by(AgentVersion.published_at.desc())
    elif sort == "popular":
        stmt = stmt.order_by(
            func.coalesce(import_count_sq.c.import_count, 0).desc(),
            AgentVersion.published_at.desc(),
        )
    else:  # name
        stmt = stmt.order_by(func.lower(AgentVersion.name).asc())

    rows = (await db.execute(stmt.offset(offset).limit(limit))).all()

    items: list[dict] = []
    for ca, v, owner_email, ic in rows:
        items.append({
            "id": str(ca.id),
            "slug": ca.slug,
            "name": v.name,
            "description": v.description,
            "icon": v.icon,
            "category": v.category,
            "owner_email": owner_email,
            "current_version_number": v.version_number,
            "current_version_id": str(v.id),
            "mcp_servers": _extract_server_names(list(v.mcp_tool_bindings or [])),
            "import_count": int(ic),
            "is_published": ca.is_published,
            "unpublished_at": ca.unpublished_at,
            "updated_at": ca.updated_at,
        })
    return items, int(total)


async def get_catalog_detail(
    db: AsyncSession, catalog_agent_id: uuid.UUID, user: User,
    version_id: uuid.UUID | None = None,
) -> dict:
    """Return the detail payload for a catalog agent.

    - If ``version_id`` is given, return that specific version (only if it's
      live, OR if the requester has an import pinned to it).
    - Else return the current_version.
    - 404 if the catalog is not visible and the requester is not the owner.
    """
    from app.db.models import CatalogImport

    row = (await db.execute(
        select(
            CatalogAgent,
            User.email.label("owner_email"),
        )
        .join(User, User.id == CatalogAgent.owner_id)
        .where(CatalogAgent.id == catalog_agent_id)
    )).one_or_none()
    if row is None:
        raise NotFoundError("Catalog agent not found")
    ca, owner_email = row

    is_owner = ca.owner_id == user.id
    if not is_owner and (not ca.is_published or ca.unpublished_at is not None):
        raise NotFoundError("Catalog agent not found")

    if version_id is not None:
        version = (await db.execute(
            select(AgentVersion).where(
                AgentVersion.id == version_id,
                AgentVersion.catalog_agent_id == ca.id,
            )
        )).scalar_one_or_none()
        if version is None:
            raise NotFoundError("Version not found")

        # An unpublished version is viewable only if (a) you're the owner or
        # (b) you have an import pinned to it.
        if version.unpublished_at is not None and not is_owner:
            has_pin = (await db.execute(
                select(CatalogImport.id)
                .join(Agent, Agent.catalog_import_id == CatalogImport.id)
                .where(
                    Agent.owner_id == user.id,
                    CatalogImport.pinned_version_id == version.id,
                )
            )).scalar_one_or_none()
            if not has_pin:
                raise NotFoundError("Version not found")
    else:
        if ca.current_version_id is None:
            raise NotFoundError("No live version")
        version = (await db.execute(
            select(AgentVersion).where(AgentVersion.id == ca.current_version_id)
        )).scalar_one()

    # Does the caller already import this catalog?
    imported_agent_id = (await db.execute(
        select(Agent.id)
        .join(CatalogImport, CatalogImport.id == Agent.catalog_import_id)
        .where(Agent.owner_id == user.id, CatalogImport.catalog_agent_id == ca.id)
    )).scalar_one_or_none()

    # Popularity.
    import_count = (await db.execute(
        select(func.count(Agent.id))
        .join(CatalogImport, CatalogImport.id == Agent.catalog_import_id)
        .where(CatalogImport.catalog_agent_id == ca.id)
    )).scalar_one() or 0

    # Reuse the version row we already loaded when it IS the current one.
    if ca.current_version_id is not None and version.id == ca.current_version_id:
        current_version_number = version.version_number
    elif ca.current_version_id is not None:
        current_version_number = (await db.execute(
            select(AgentVersion.version_number).where(
                AgentVersion.id == ca.current_version_id
            )
        )).scalar_one_or_none()
    else:
        current_version_number = None

    return {
        "id": str(ca.id),
        "slug": ca.slug,
        "owner_id": str(ca.owner_id),
        "owner_email": owner_email,
        "is_published": ca.is_published,
        "unpublished_at": ca.unpublished_at,
        "current_version_id": (
            str(ca.current_version_id) if ca.current_version_id else None
        ),
        "current_version_number": current_version_number,
        "created_at": ca.created_at,
        "updated_at": ca.updated_at,
        "import_count": int(import_count),
        "version": version,
        "imported_as_agent_id": (
            str(imported_agent_id) if imported_agent_id else None
        ),
    }


async def list_versions(
    db: AsyncSession, catalog_agent_id: uuid.UUID, *, include_unpublished: bool,
) -> tuple[list[AgentVersion], int]:
    stmt = select(AgentVersion).where(
        AgentVersion.catalog_agent_id == catalog_agent_id
    )
    if not include_unpublished:
        stmt = stmt.where(AgentVersion.unpublished_at.is_(None))
    stmt = stmt.order_by(AgentVersion.version_number.desc())

    rows = (await db.execute(stmt)).scalars().all()
    return list(rows), len(rows)


async def compute_facets(
    db: AsyncSession,
    *,
    q: str | None,
    categories: list[str] | None,
    mcp_servers: list[str] | None,
) -> dict:
    """Facet counts over the live (published) catalog.

    Both facets exclude their own selection so toggling one option doesn't
    zero out its siblings — standard browse-facet behaviour.
    """
    def _narrow(stmt, include_categories: bool, include_servers: bool):
        stmt = _live_join(stmt)
        if q:
            pattern = f"%{q}%"
            stmt = stmt.where(
                or_(
                    AgentVersion.name.ilike(pattern),
                    AgentVersion.description.ilike(pattern),
                )
            )
        if include_categories and categories:
            stmt = stmt.where(AgentVersion.category.in_(categories))
        if include_servers and mcp_servers:
            from sqlalchemy import cast
            from sqlalchemy.dialects.postgresql import JSONB

            conds = [
                AgentVersion.mcp_tool_bindings.op("@>")(
                    cast([{"server_name": srv}], JSONB)
                )
                for srv in mcp_servers
            ]
            stmt = stmt.where(or_(*conds))
        return stmt

    cat_stmt = _narrow(
        select(AgentVersion.category, func.count(CatalogAgent.id)).select_from(
            CatalogAgent
        ),
        include_categories=False,
        include_servers=True,
    ).group_by(AgentVersion.category)
    cat_rows = (await db.execute(cat_stmt)).all()
    categories_facet = [
        {"name": name, "count": int(cnt)} for name, cnt in cat_rows
    ]

    srv_stmt = _narrow(
        select(AgentVersion.mcp_tool_bindings).select_from(CatalogAgent),
        include_categories=True,
        include_servers=False,
    )
    server_counts: dict[str, int] = {}
    for (bindings,) in (await db.execute(srv_stmt)).all():
        for s in _extract_server_names(list(bindings or [])):
            server_counts[s] = server_counts.get(s, 0) + 1
    servers_facet = sorted(
        ({"name": n, "count": c} for n, c in server_counts.items()),
        key=lambda r: (-r["count"], r["name"]),
    )

    return {"categories": categories_facet, "servers": servers_facet}


async def _hydrate_my_catalog_row(
    db: AsyncSession, user: User, ca: CatalogAgent
) -> dict:
    version: AgentVersion | None = None
    if ca.current_version_id is not None:
        version = (await db.execute(
            select(AgentVersion).where(AgentVersion.id == ca.current_version_id)
        )).scalar_one_or_none()

    linked_agent_id = (await db.execute(
        select(Agent.id).where(
            Agent.owner_id == user.id,
            Agent.linked_catalog_agent_id == ca.id,
            Agent.catalog_import_id.is_(None),
        ).limit(1)
    )).scalar_one_or_none()

    from app.db.models import CatalogImport
    import_count = (await db.execute(
        select(func.count(Agent.id))
        .join(CatalogImport, CatalogImport.id == Agent.catalog_import_id)
        .where(CatalogImport.catalog_agent_id == ca.id)
    )).scalar_one() or 0

    return {
        "id": str(ca.id),
        "slug": ca.slug,
        "name": version.name if version else ca.slug,
        "description": version.description if version else None,
        "icon": version.icon if version else None,
        "category": ca.category,
        "is_published": ca.is_published,
        "unpublished_at": ca.unpublished_at,
        "current_version_number": version.version_number if version else None,
        "linked_agent_id": str(linked_agent_id) if linked_agent_id else None,
        "import_count": int(import_count),
        "created_at": ca.created_at,
        "updated_at": ca.updated_at,
    }


async def get_my_catalog_item(
    db: AsyncSession, user: User, catalog_agent_id: uuid.UUID
) -> dict | None:
    """Single hydrated /catalog/mine row for one catalog agent owned by user."""
    ca = (await db.execute(
        select(CatalogAgent).where(
            CatalogAgent.id == catalog_agent_id, CatalogAgent.owner_id == user.id
        )
    )).scalar_one_or_none()
    if ca is None:
        return None
    return await _hydrate_my_catalog_row(db, user, ca)


async def list_my_catalog(
    db: AsyncSession, user: User, offset: int, limit: int
) -> tuple[list[dict], int]:
    """Catalog entries owned by the caller (including unpublished)."""
    base = select(CatalogAgent).where(CatalogAgent.owner_id == user.id)
    total = (await db.execute(
        select(func.count()).select_from(base.subquery())
    )).scalar_one()

    catalog_agents = (await db.execute(
        base.order_by(CatalogAgent.updated_at.desc()).offset(offset).limit(limit)
    )).scalars().all()

    items = [await _hydrate_my_catalog_row(db, user, ca) for ca in catalog_agents]
    return items, int(total)
