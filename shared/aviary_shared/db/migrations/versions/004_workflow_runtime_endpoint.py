"""Per-workflow runtime_endpoint override.

Mirrors the existing agent.runtime_endpoint pattern. NULL = use the
supervisor's configured default environment.

Revision ID: 004
Revises: 003
Create Date: 2026-04-18
"""
from typing import Sequence, Union

import sqlalchemy as sa
from alembic import op

revision: str = "004"
down_revision: Union[str, None] = "003"
branch_labels: Union[str, Sequence[str], None] = None
depends_on: Union[str, Sequence[str], None] = None


def upgrade() -> None:
    # Earlier baselines call Base.metadata.create_all with the live ORM,
    # so a fresh DB already has this column by the time 004 runs.
    bind = op.get_bind()
    cols = {c["name"] for c in sa.inspect(bind).get_columns("workflows")}
    if "runtime_endpoint" not in cols:
        op.add_column(
            "workflows",
            sa.Column("runtime_endpoint", sa.String(length=512), nullable=True),
        )


def downgrade() -> None:
    bind = op.get_bind()
    cols = {c["name"] for c in sa.inspect(bind).get_columns("workflows")}
    if "runtime_endpoint" in cols:
        op.drop_column("workflows", "runtime_endpoint")
