"""MCP Gateway models — McpServer, McpTool.

Kept intentionally minimal for Phase 2. ACL + agent→tool binding are deferred
to the RBAC redesign. `is_platform_provided` distinguishes platform-managed
servers (accessible to every authenticated user) from user-registered servers
(currently denied at the gateway until RBAC lands).
"""

from __future__ import annotations

import uuid
from datetime import datetime

from sqlalchemy import Boolean, ForeignKey, String, Text, UniqueConstraint, func
from sqlalchemy.dialects.postgresql import JSONB, TIMESTAMP, UUID
from sqlalchemy.orm import Mapped, mapped_column, relationship

from aviary_shared.db.models.base import Base


class McpServer(Base):
    __tablename__ = "mcp_servers"

    id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), primary_key=True, default=uuid.uuid4,
        server_default=func.gen_random_uuid(),
    )
    name: Mapped[str] = mapped_column(String(255), unique=True, nullable=False)
    description: Mapped[str | None] = mapped_column(Text, nullable=True)
    transport_type: Mapped[str] = mapped_column(
        String(20), nullable=False, default="streamable_http",
        server_default="streamable_http",
    )
    connection_config: Mapped[dict] = mapped_column(JSONB, nullable=False, server_default="{}")
    tags: Mapped[list] = mapped_column(JSONB, server_default="[]")
    is_platform_provided: Mapped[bool] = mapped_column(
        Boolean, default=False, server_default="false", nullable=False,
    )
    status: Mapped[str] = mapped_column(
        String(20), nullable=False, default="active", server_default="active",
    )
    last_discovered_at: Mapped[datetime | None] = mapped_column(
        TIMESTAMP(timezone=True), nullable=True,
    )
    created_at: Mapped[datetime] = mapped_column(
        TIMESTAMP(timezone=True), server_default=func.now(), nullable=False,
    )
    updated_at: Mapped[datetime] = mapped_column(
        TIMESTAMP(timezone=True), server_default=func.now(),
        onupdate=func.now(), nullable=False,
    )

    tools: Mapped[list["McpTool"]] = relationship(
        back_populates="server", cascade="all, delete-orphan", passive_deletes=True,
    )


class McpTool(Base):
    __tablename__ = "mcp_tools"
    __table_args__ = (UniqueConstraint("server_id", "name", name="uq_mcp_tool_server_name"),)

    id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), primary_key=True, default=uuid.uuid4,
        server_default=func.gen_random_uuid(),
    )
    server_id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True),
        ForeignKey("mcp_servers.id", ondelete="CASCADE"),
        nullable=False,
    )
    name: Mapped[str] = mapped_column(String(255), nullable=False)
    description: Mapped[str | None] = mapped_column(Text, nullable=True)
    input_schema: Mapped[dict] = mapped_column(JSONB, server_default="{}")
    created_at: Mapped[datetime] = mapped_column(
        TIMESTAMP(timezone=True), server_default=func.now(), nullable=False,
    )

    server: Mapped["McpServer"] = relationship(back_populates="tools")
