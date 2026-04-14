"""Workflow versions table for deploy history

Revision ID: 007
Revises: 006
Create Date: 2026-04-12
"""
from typing import Sequence, Union

import sqlalchemy as sa
from alembic import op
from sqlalchemy.dialects.postgresql import JSONB, UUID

revision: str = "007"
down_revision: Union[str, None] = "006"
branch_labels: Union[str, Sequence[str], None] = None
depends_on: Union[str, Sequence[str], None] = None


def upgrade() -> None:
    op.create_table(
        "workflow_versions",
        sa.Column("id", UUID(as_uuid=True), server_default=sa.text("gen_random_uuid()"), primary_key=True),
        sa.Column(
            "workflow_id", UUID(as_uuid=True),
            sa.ForeignKey("workflows.id", ondelete="CASCADE"), nullable=False,
        ),
        sa.Column("version", sa.Integer, nullable=False),
        sa.Column("definition_snapshot", JSONB, nullable=False),
        sa.Column(
            "deployed_by", UUID(as_uuid=True),
            sa.ForeignKey("users.id"), nullable=False,
        ),
        sa.Column("deployed_at", sa.TIMESTAMP(timezone=True), server_default=sa.text("now()")),
        sa.UniqueConstraint("workflow_id", "version", name="uq_workflow_version"),
    )


def downgrade() -> None:
    op.drop_table("workflow_versions")
