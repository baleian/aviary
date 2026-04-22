"""Catalog-related Pydantic schemas."""

from __future__ import annotations

from datetime import datetime
from enum import Enum

from pydantic import BaseModel, ConfigDict, Field

from app.schemas._common import MODEL_CONFIG_ALIAS, OptionalUuidStr, UuidStr


class CatalogCategory(str, Enum):
    """Controlled vocabulary for catalog filtering — can grow without DB migration
    because storage side uses VARCHAR(32)."""

    coding = "coding"
    research = "research"
    writing = "writing"
    productivity = "productivity"
    marketing = "marketing"
    data = "data"
    ops = "ops"
    other = "other"


class PublishRequest(BaseModel):
    category: CatalogCategory
    release_notes: str | None = None


class DriftResponse(BaseModel):
    is_dirty: bool
    latest_version_number: int | None = None
    latest_version_id: OptionalUuidStr = None
    changed_fields: list[str] = []


class McpToolBinding(BaseModel):
    server_name: str
    tool_name: str


class AgentVersionResponse(BaseModel):
    model_config = ConfigDict(
        from_attributes=True, populate_by_name=True, protected_namespaces=(),
    )

    id: UuidStr
    catalog_agent_id: UuidStr
    version_number: int
    name: str
    description: str | None = None
    icon: str | None = None
    instruction: str
    model_config_json: dict = Field(**MODEL_CONFIG_ALIAS)
    tools: list
    mcp_servers: list
    mcp_tool_bindings: list[McpToolBinding]
    category: str
    release_notes: str | None = None
    published_by: UuidStr
    published_at: datetime
    unpublished_at: datetime | None = None


class AgentVersionSummary(BaseModel):
    """Lightweight version descriptor for history lists."""

    model_config = ConfigDict(from_attributes=True, populate_by_name=True)

    id: UuidStr
    version_number: int
    release_notes: str | None = None
    published_at: datetime
    unpublished_at: datetime | None = None


class AgentVersionListResponse(BaseModel):
    items: list[AgentVersionSummary]
    total: int


class CatalogAgentSummary(BaseModel):
    """Shape used in the /catalog list view — hydrated from JOIN with the
    current AgentVersion snapshot."""

    model_config = ConfigDict(populate_by_name=True, protected_namespaces=())

    id: UuidStr
    slug: str
    name: str
    description: str | None = None
    icon: str | None = None
    category: str
    owner_email: str
    current_version_number: int
    current_version_id: UuidStr
    mcp_servers: list[str] = []  # unique server names extracted from mcp_tool_bindings
    import_count: int = 0
    is_published: bool
    unpublished_at: datetime | None = None
    updated_at: datetime


class CatalogListResponse(BaseModel):
    items: list[CatalogAgentSummary]
    total: int


class CatalogAgentDetail(BaseModel):
    """Full detail for the catalog detail page — combines CatalogAgent
    identity with the current (or requested) AgentVersion snapshot."""

    model_config = ConfigDict(populate_by_name=True, protected_namespaces=())

    id: UuidStr
    slug: str
    owner_id: UuidStr
    owner_email: str
    is_published: bool
    unpublished_at: datetime | None = None
    current_version_id: OptionalUuidStr = None
    current_version_number: int | None = None
    created_at: datetime
    updated_at: datetime
    import_count: int = 0
    # The effective version being viewed (may be latest or a specific version
    # when the request asked for one).
    version: AgentVersionResponse
    # Whether the requester currently has an import for this catalog agent.
    imported_as_agent_id: OptionalUuidStr = None


class CategoryFacet(BaseModel):
    name: str
    count: int


class ServerFacet(BaseModel):
    name: str
    count: int


class CatalogFacetsResponse(BaseModel):
    categories: list[CategoryFacet]
    servers: list[ServerFacet]


# ── Imports ──────────────────────────────────────────────────────────────

class ImportItem(BaseModel):
    catalog_agent_id: UuidStr
    pinned_version_id: OptionalUuidStr = None


class ImportRequest(BaseModel):
    items: list[ImportItem]


class ImportPatchRequest(BaseModel):
    pinned_version_id: OptionalUuidStr = None


# ── Publisher-facing ─────────────────────────────────────────────────────

class RollbackRequest(BaseModel):
    version_id: UuidStr


class MyCatalogAgent(BaseModel):
    """An entry in GET /catalog/mine."""

    model_config = ConfigDict(populate_by_name=True, protected_namespaces=())

    id: UuidStr
    slug: str
    name: str
    description: str | None = None
    icon: str | None = None
    category: str
    is_published: bool
    unpublished_at: datetime | None = None
    current_version_number: int | None = None
    linked_agent_id: OptionalUuidStr = None
    import_count: int = 0
    created_at: datetime
    updated_at: datetime


class MyCatalogListResponse(BaseModel):
    items: list[MyCatalogAgent]
    total: int
