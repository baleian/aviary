"""Agent model — owner-only access; ACL will be re-introduced later under RBAC."""

from __future__ import annotations

import uuid
from datetime import datetime

from sqlalchemy import ForeignKey, String, Text, UniqueConstraint, func
from sqlalchemy.dialects.postgresql import JSONB, TIMESTAMP, UUID
from sqlalchemy.orm import Mapped, mapped_column, relationship

from aviary_shared.db.models.base import Base


class Agent(Base):
    __tablename__ = "agents"
    __table_args__ = (
        # Slug is unique per owner, not globally — a consumer may import a
        # catalog agent whose slug matches another user's private agent.
        UniqueConstraint("owner_id", "slug", name="uq_agents_owner_slug"),
    )

    id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), primary_key=True, default=uuid.uuid4, server_default=func.gen_random_uuid()
    )
    name: Mapped[str] = mapped_column(String(255), nullable=False)
    slug: Mapped[str] = mapped_column(String(255), nullable=False)
    description: Mapped[str | None] = mapped_column(Text, nullable=True)
    owner_id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), ForeignKey("users.id"), nullable=False
    )

    instruction: Mapped[str] = mapped_column(Text, nullable=False)
    model_config_json: Mapped[dict] = mapped_column(
        "model_config", JSONB, nullable=False, server_default="{}"
    )
    tools: Mapped[list] = mapped_column(JSONB, server_default="[]")
    mcp_servers: Mapped[list] = mapped_column(JSONB, server_default="[]")

    icon: Mapped[str | None] = mapped_column(String(50), nullable=True)

    # Optional per-agent runtime endpoint override. NULL → caller falls back to
    # the supervisor's configured default environment endpoint.
    runtime_endpoint: Mapped[str | None] = mapped_column(String(512), nullable=True)

    # Catalog linkage. `linked_catalog_agent_id` is set on both publisher working
    # copies and consumer imports — editor vs viewer is derived by comparing
    # catalog_agent.owner_id with agents.owner_id.
    # `catalog_import_id` is set only on consumer imports (version subscription).
    linked_catalog_agent_id: Mapped[uuid.UUID | None] = mapped_column(
        UUID(as_uuid=True),
        ForeignKey("catalog_agents.id", ondelete="SET NULL"),
        nullable=True,
    )
    catalog_import_id: Mapped[uuid.UUID | None] = mapped_column(
        UUID(as_uuid=True),
        ForeignKey("catalog_imports.id", ondelete="SET NULL"),
        nullable=True,
    )

    status: Mapped[str] = mapped_column(String(20), default="active", server_default="active")
    created_at: Mapped[datetime] = mapped_column(
        TIMESTAMP(timezone=True), server_default=func.now()
    )
    updated_at: Mapped[datetime] = mapped_column(
        TIMESTAMP(timezone=True), server_default=func.now(), onupdate=func.now()
    )

    owner: Mapped["User"] = relationship(back_populates="owned_agents")  # noqa: F821
    sessions: Mapped[list["Session"]] = relationship(back_populates="agent")  # noqa: F821
    linked_catalog_agent: Mapped["CatalogAgent | None"] = relationship(  # noqa: F821
        foreign_keys=[linked_catalog_agent_id]
    )
    catalog_import: Mapped["CatalogImport | None"] = relationship(  # noqa: F821
        foreign_keys=[catalog_import_id]
    )
