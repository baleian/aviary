"""Slug resolver for the per-user agents namespace.

With catalog imports sharing the `agents` table as rows, uniqueness within
a user's reference space is enforced by the DB constraint
`UNIQUE(owner_id, slug)`. On conflict we append a numeric suffix.
"""

from __future__ import annotations

import uuid

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.db.models import Agent


async def resolve_available_slug(
    db: AsyncSession, owner_id: uuid.UUID, desired: str
) -> str:
    """Return ``desired`` if free, else append -2, -3, … up to 100; finally
    fall back to ``<desired>-<short-uuid>``."""

    def _query(slug: str):
        return select(Agent.id).where(
            Agent.owner_id == owner_id, Agent.slug == slug
        )

    if not (await db.execute(_query(desired))).scalar_one_or_none():
        return desired

    for n in range(2, 101):
        candidate = f"{desired}-{n}"
        if not (await db.execute(_query(candidate))).scalar_one_or_none():
            return candidate

    short = uuid.uuid4().hex[:8]
    return f"{desired}-{short}"
