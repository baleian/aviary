"""Drop legacy agents.mcp_servers column.

Direct (non-gateway) MCP servers were a fallback for `MCP_GATEWAY_URL`-less
deployments. With the gateway now mandatory, the column is unused.

Idempotent — fresh DBs created by migration 001 (`create_all` against the
current ORM) never had the column, so we IF EXISTS it.

Revision ID: 002
Revises: 001
Create Date: 2026-05-05
"""
from typing import Sequence, Union

from alembic import op

revision: str = "002"
down_revision: Union[str, None] = "001"
branch_labels: Union[str, Sequence[str], None] = None
depends_on: Union[str, Sequence[str], None] = None


def upgrade() -> None:
    op.execute("ALTER TABLE agents DROP COLUMN IF EXISTS mcp_servers")


def downgrade() -> None:
    op.execute(
        "ALTER TABLE agents ADD COLUMN IF NOT EXISTS mcp_servers JSONB "
        "NOT NULL DEFAULT '[]'::jsonb"
    )
