"""Add agents.description; coerce legacy tools={} dict rows to NULL.

`tools` was historically typed as `dict | None` (free-form JSONB); the
schema is now `list[str] | None`, so any pre-existing dict values would
fail response validation. Phase 1 dev rows are normalized to NULL here.

Revision ID: 005
Revises: 004
Create Date: 2026-04-14
"""
from typing import Sequence, Union

from alembic import op
import sqlalchemy as sa

revision: str = "005"
down_revision: Union[str, None] = "004"
branch_labels: Union[str, Sequence[str], None] = None
depends_on: Union[str, Sequence[str], None] = None


def upgrade() -> None:
    op.add_column("agents", sa.Column("description", sa.String(), nullable=True))
    op.execute(
        "UPDATE agents SET tools = NULL "
        "WHERE tools IS NOT NULL AND jsonb_typeof(tools) <> 'array'"
    )


def downgrade() -> None:
    op.drop_column("agents", "description")
