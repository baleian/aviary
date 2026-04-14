"""Agent entity."""

import uuid
from datetime import datetime

from sqlalchemy import String, func
from sqlalchemy.dialects.postgresql import JSONB, TIMESTAMP, UUID
from sqlalchemy.orm import Mapped, mapped_column, relationship

from aviary_shared.db.models.base import Base


class Agent(Base):
    __tablename__ = "agents"

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    name: Mapped[str] = mapped_column(String(255))
    slug: Mapped[str] = mapped_column(String(255), unique=True)
    owner_id: Mapped[str] = mapped_column(String(255))
    description: Mapped[str | None] = mapped_column(String, nullable=True)
    instruction: Mapped[str | None] = mapped_column(String, nullable=True)
    model_config_data: Mapped[dict | None] = mapped_column("model_config", JSONB, nullable=True)
    tools: Mapped[list | None] = mapped_column(JSONB, nullable=True)
    mcp_tool_ids: Mapped[list] = mapped_column(JSONB, nullable=False, server_default="[]", default=list)
    status: Mapped[str] = mapped_column(String(50), default="active")
    created_at: Mapped[datetime] = mapped_column(TIMESTAMP(timezone=True), server_default=func.now())
    updated_at: Mapped[datetime] = mapped_column(TIMESTAMP(timezone=True), server_default=func.now(), onupdate=func.now())

    # One-to-one inverse of Policy.agent_id (ON DELETE CASCADE at the DB
    # level). `delete-orphan` also propagates ORM-level deletes.
    policy: Mapped["Policy | None"] = relationship(
        "Policy", back_populates="agent", uselist=False,
        cascade="all, delete-orphan", lazy="joined",
    )
