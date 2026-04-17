"""MCP models — per-agent tool bindings.

LiteLLM owns the MCP server + tool catalog and all access control on them.
Aviary only remembers which ``(server, tool)`` pairs each agent is bound to
— stored as strings so the binding survives LiteLLM server re-registration
as long as the name stays stable.
"""

from __future__ import annotations

import uuid
from datetime import datetime

from sqlalchemy import ForeignKey, String, UniqueConstraint, func
from sqlalchemy.dialects.postgresql import TIMESTAMP, UUID
from sqlalchemy.orm import Mapped, mapped_column

from aviary_shared.db.models.base import Base


class McpAgentToolBinding(Base):
    __tablename__ = "mcp_agent_tool_bindings"
    __table_args__ = (
        UniqueConstraint(
            "agent_id",
            "server_name",
            "tool_name",
            name="uq_mcp_binding_agent_server_tool",
        ),
    )

    id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True),
        primary_key=True,
        default=uuid.uuid4,
        server_default=func.gen_random_uuid(),
    )
    agent_id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True),
        ForeignKey("agents.id", ondelete="CASCADE"),
        nullable=False,
    )
    # LiteLLM-exposed names. `server_name` is the alias under `mcp_servers:` in
    # litellm config.yaml (or the `server_name` of a REST-registered server);
    # `tool_name` is the backend tool name (no prefix).
    server_name: Mapped[str] = mapped_column(String(255), nullable=False)
    tool_name: Mapped[str] = mapped_column(String(255), nullable=False)
    created_at: Mapped[datetime] = mapped_column(
        TIMESTAMP(timezone=True), server_default=func.now()
    )
