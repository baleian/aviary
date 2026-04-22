"""Editor vs viewer decision for catalog-linked agents.

Kept as a separate module so future RBAC expansion (owners list on
CatalogAgent) only needs to change one line.
"""

from __future__ import annotations

from app.db.models import Agent, CatalogAgent


def is_catalog_editor(agent: Agent, catalog_agent: CatalogAgent) -> bool:
    return catalog_agent.owner_id == agent.owner_id
