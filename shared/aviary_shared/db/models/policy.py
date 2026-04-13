"""Infrastructure policy entity — 1:1 with Agent (owned by, lives and dies with)."""

import uuid
from datetime import datetime

from sqlalchemy import ForeignKey, func
from sqlalchemy.dialects.postgresql import JSONB, TIMESTAMP, UUID
from sqlalchemy.orm import Mapped, mapped_column, relationship

from aviary_shared.db.models.base import Base


class Policy(Base):
    __tablename__ = "policies"

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    agent_id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True),
        ForeignKey("agents.id", ondelete="CASCADE"),
        unique=True,
    )
    min_tasks: Mapped[int] = mapped_column(default=0)
    max_tasks: Mapped[int] = mapped_column(default=1)
    resource_limits: Mapped[dict | None] = mapped_column(JSONB, nullable=True)
    policy_rules: Mapped[dict | None] = mapped_column(JSONB, nullable=True)
    last_activity_at: Mapped[datetime | None] = mapped_column(TIMESTAMP(timezone=True), nullable=True)
    created_at: Mapped[datetime] = mapped_column(TIMESTAMP(timezone=True), server_default=func.now())
    updated_at: Mapped[datetime] = mapped_column(TIMESTAMP(timezone=True), server_default=func.now(), onupdate=func.now())

    agent = relationship("Agent", back_populates="policy")
