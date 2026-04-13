"""Phase 2: mcp_servers + mcp_tools.

Revision ID: 006
Revises: 005
Create Date: 2026-04-14
"""
from typing import Sequence, Union

from alembic import op
import sqlalchemy as sa
from sqlalchemy.dialects import postgresql

revision: str = "006"
down_revision: Union[str, None] = "005"
branch_labels: Union[str, Sequence[str], None] = None
depends_on: Union[str, Sequence[str], None] = None


def upgrade() -> None:
    op.create_table(
        "mcp_servers",
        sa.Column("id", postgresql.UUID(as_uuid=True), primary_key=True,
                  server_default=sa.text("gen_random_uuid()")),
        sa.Column("name", sa.String(255), nullable=False, unique=True),
        sa.Column("description", sa.Text(), nullable=True),
        sa.Column("transport_type", sa.String(20), nullable=False,
                  server_default="streamable_http"),
        sa.Column("connection_config", postgresql.JSONB(), nullable=False,
                  server_default=sa.text("'{}'::jsonb")),
        sa.Column("tags", postgresql.JSONB(), nullable=True,
                  server_default=sa.text("'[]'::jsonb")),
        sa.Column("is_platform_provided", sa.Boolean(), nullable=False,
                  server_default=sa.text("false")),
        sa.Column("status", sa.String(20), nullable=False, server_default="active"),
        sa.Column("last_discovered_at", postgresql.TIMESTAMP(timezone=True), nullable=True),
        sa.Column("created_at", postgresql.TIMESTAMP(timezone=True),
                  server_default=sa.func.now(), nullable=False),
        sa.Column("updated_at", postgresql.TIMESTAMP(timezone=True),
                  server_default=sa.func.now(), nullable=False),
    )
    op.create_table(
        "mcp_tools",
        sa.Column("id", postgresql.UUID(as_uuid=True), primary_key=True,
                  server_default=sa.text("gen_random_uuid()")),
        sa.Column("server_id", postgresql.UUID(as_uuid=True),
                  sa.ForeignKey("mcp_servers.id", ondelete="CASCADE"), nullable=False),
        sa.Column("name", sa.String(255), nullable=False),
        sa.Column("description", sa.Text(), nullable=True),
        sa.Column("input_schema", postgresql.JSONB(), nullable=True,
                  server_default=sa.text("'{}'::jsonb")),
        sa.Column("created_at", postgresql.TIMESTAMP(timezone=True),
                  server_default=sa.func.now(), nullable=False),
        sa.UniqueConstraint("server_id", "name", name="uq_mcp_tool_server_name"),
    )


def downgrade() -> None:
    op.drop_table("mcp_tools")
    op.drop_table("mcp_servers")
