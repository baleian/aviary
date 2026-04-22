"""Agent Catalog — CatalogAgent / AgentVersion / CatalogImport.

CatalogAgent is an independent entity from the private Agent. A user's private
Agent may link to a CatalogAgent as a working copy (`linked_catalog_agent_id`)
or as a read-only consumer subscription (`catalog_import_id`). Editor vs viewer
is derived from `catalog_agents.owner_id == agents.owner_id`, not from FK flags.
"""

from __future__ import annotations

import uuid
from datetime import datetime

from sqlalchemy import ForeignKey, Integer, String, Text, func
from sqlalchemy.dialects.postgresql import JSONB, TIMESTAMP, UUID
from sqlalchemy.orm import Mapped, mapped_column, relationship

from aviary_shared.db.models.base import Base


class CatalogAgent(Base):
    __tablename__ = "catalog_agents"

    id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True),
        primary_key=True,
        default=uuid.uuid4,
        server_default=func.gen_random_uuid(),
    )
    owner_id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), ForeignKey("users.id", ondelete="RESTRICT"), nullable=False
    )
    slug: Mapped[str] = mapped_column(String(255), nullable=False)
    # current_version_id points to the "latest" AgentVersion. Circular FK resolved
    # with use_alter=True in the migration; SET NULL on version cascade.
    current_version_id: Mapped[uuid.UUID | None] = mapped_column(
        UUID(as_uuid=True),
        ForeignKey("agent_versions.id", use_alter=True, name="fk_catalog_agents_current_version"),
        nullable=True,
    )
    is_published: Mapped[bool] = mapped_column(
        nullable=False, default=True, server_default="true"
    )
    category: Mapped[str] = mapped_column(String(32), nullable=False)
    unpublished_at: Mapped[datetime | None] = mapped_column(
        TIMESTAMP(timezone=True), nullable=True
    )
    created_at: Mapped[datetime] = mapped_column(
        TIMESTAMP(timezone=True), server_default=func.now()
    )
    updated_at: Mapped[datetime] = mapped_column(
        TIMESTAMP(timezone=True), server_default=func.now(), onupdate=func.now()
    )

    owner: Mapped["User"] = relationship()  # noqa: F821
    versions: Mapped[list["AgentVersion"]] = relationship(
        back_populates="catalog_agent",
        foreign_keys="AgentVersion.catalog_agent_id",
        cascade="all, delete-orphan",
        passive_deletes=True,
    )
    current_version: Mapped["AgentVersion | None"] = relationship(
        foreign_keys=[current_version_id], post_update=True
    )


class AgentVersion(Base):
    __tablename__ = "agent_versions"

    id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True),
        primary_key=True,
        default=uuid.uuid4,
        server_default=func.gen_random_uuid(),
    )
    catalog_agent_id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True),
        ForeignKey("catalog_agents.id", ondelete="CASCADE"),
        nullable=False,
    )
    version_number: Mapped[int] = mapped_column(Integer, nullable=False)

    # Immutable snapshot of the working copy at publish time.
    name: Mapped[str] = mapped_column(String(255), nullable=False)
    description: Mapped[str | None] = mapped_column(Text, nullable=True)
    icon: Mapped[str | None] = mapped_column(String(50), nullable=True)
    instruction: Mapped[str] = mapped_column(Text, nullable=False)
    model_config_json: Mapped[dict] = mapped_column(
        "model_config", JSONB, nullable=False, server_default="{}"
    )
    tools: Mapped[list] = mapped_column(JSONB, nullable=False, server_default="[]")
    mcp_servers: Mapped[list] = mapped_column(JSONB, nullable=False, server_default="[]")
    # Snapshot of (server_name, tool_name) bindings at publish time.
    mcp_tool_bindings: Mapped[list] = mapped_column(
        JSONB, nullable=False, server_default="[]"
    )
    category: Mapped[str] = mapped_column(String(32), nullable=False)

    release_notes: Mapped[str | None] = mapped_column(Text, nullable=True)
    published_by: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), ForeignKey("users.id"), nullable=False
    )
    published_at: Mapped[datetime] = mapped_column(
        TIMESTAMP(timezone=True), server_default=func.now()
    )
    unpublished_at: Mapped[datetime | None] = mapped_column(
        TIMESTAMP(timezone=True), nullable=True
    )

    catalog_agent: Mapped["CatalogAgent"] = relationship(
        back_populates="versions", foreign_keys=[catalog_agent_id]
    )
    publisher: Mapped["User"] = relationship(foreign_keys=[published_by])  # noqa: F821


class CatalogImport(Base):
    """Version-subscription relationship between an imported agents row and a CatalogAgent.

    Always 1:1 with an `agents` row via `agents.catalog_import_id`. This table
    holds only the subscription metadata. No user_id or slug — both live on the
    owning agents row.
    """

    __tablename__ = "catalog_imports"

    id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True),
        primary_key=True,
        default=uuid.uuid4,
        server_default=func.gen_random_uuid(),
    )
    catalog_agent_id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True),
        ForeignKey("catalog_agents.id", ondelete="RESTRICT"),
        nullable=False,
    )
    # NULL → follow latest (catalog.current_version_id), UUID → pinned to version.
    pinned_version_id: Mapped[uuid.UUID | None] = mapped_column(
        UUID(as_uuid=True), ForeignKey("agent_versions.id"), nullable=True
    )
    imported_at: Mapped[datetime] = mapped_column(
        TIMESTAMP(timezone=True), server_default=func.now()
    )
    updated_at: Mapped[datetime] = mapped_column(
        TIMESTAMP(timezone=True), server_default=func.now(), onupdate=func.now()
    )

    catalog_agent: Mapped["CatalogAgent"] = relationship()
    pinned_version: Mapped["AgentVersion | None"] = relationship(
        foreign_keys=[pinned_version_id]
    )
