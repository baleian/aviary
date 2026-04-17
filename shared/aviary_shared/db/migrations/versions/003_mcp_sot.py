"""LiteLLM as single source of truth for MCP.

Drops Aviary's MCP server + tool catalog (now owned by LiteLLM's
``LiteLLM_MCPServerTable``) and rebuilds ``mcp_agent_tool_bindings`` with
string-keyed (server_name, tool_name) columns. Per-user access control is
LiteLLM's responsibility — no Aviary-side ACL table.

POC dev environment — data loss on the dropped tables and existing bindings
is intentional (users rebind via the updated UI).

Revision ID: 003
Revises: 002
Create Date: 2026-04-17
"""
from typing import Sequence, Union

from alembic import op

from aviary_shared.db.models import Base

revision: str = "003"
down_revision: Union[str, None] = "002"
branch_labels: Union[str, Sequence[str], None] = None
depends_on: Union[str, Sequence[str], None] = None


def upgrade() -> None:
    # Wipe the old catalog; CASCADE removes the FK-dependent binding rows.
    op.execute("DROP TABLE IF EXISTS mcp_agent_tool_bindings CASCADE")
    op.execute("DROP TABLE IF EXISTS mcp_tools CASCADE")
    op.execute("DROP TABLE IF EXISTS mcp_servers CASCADE")
    # Recreate from the updated ORM — `checkfirst=True` skips existing tables.
    Base.metadata.create_all(bind=op.get_bind(), checkfirst=True)


def downgrade() -> None:
    op.execute("DROP TABLE IF EXISTS mcp_agent_tool_bindings CASCADE")
