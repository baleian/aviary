"""Phase 2: agents.mcp_tool_ids JSONB array.

Stores the user's selected MCP tool UUIDs (from the catalog). Per-agent
tool filtering against this list is deferred to RBAC; for now this is
simply the source of truth for the agent edit form's tool picker.

Revision ID: 007
Revises: 006
Create Date: 2026-04-14
"""
from typing import Sequence, Union

from alembic import op
import sqlalchemy as sa
from sqlalchemy.dialects import postgresql

revision: str = "007"
down_revision: Union[str, None] = "006"
branch_labels: Union[str, Sequence[str], None] = None
depends_on: Union[str, Sequence[str], None] = None


def upgrade() -> None:
    op.add_column(
        "agents",
        sa.Column(
            "mcp_tool_ids",
            postgresql.JSONB(),
            nullable=False,
            server_default=sa.text("'[]'::jsonb"),
        ),
    )


def downgrade() -> None:
    op.drop_column("agents", "mcp_tool_ids")
