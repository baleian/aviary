"""Phase 2: ON DELETE CASCADE on sessions.agent_id and messages.session_id.

Lets the agent hard-delete path rely on the DB to clean up dependent rows
in one statement instead of deleting each table manually. Policy is the
parent of agent (agents.policy_id → policies.id), so it's still cleaned
up explicitly in the service layer.

Revision ID: 003
Revises: 002
Create Date: 2026-04-14
"""
from typing import Sequence, Union

from alembic import op

revision: str = "003"
down_revision: Union[str, None] = "002"
branch_labels: Union[str, Sequence[str], None] = None
depends_on: Union[str, Sequence[str], None] = None


def upgrade() -> None:
    op.drop_constraint("messages_session_id_fkey", "messages", type_="foreignkey")
    op.create_foreign_key(
        "messages_session_id_fkey", "messages", "sessions",
        ["session_id"], ["id"], ondelete="CASCADE",
    )
    op.drop_constraint("sessions_agent_id_fkey", "sessions", type_="foreignkey")
    op.create_foreign_key(
        "sessions_agent_id_fkey", "sessions", "agents",
        ["agent_id"], ["id"], ondelete="CASCADE",
    )


def downgrade() -> None:
    op.drop_constraint("sessions_agent_id_fkey", "sessions", type_="foreignkey")
    op.create_foreign_key(
        "sessions_agent_id_fkey", "sessions", "agents",
        ["agent_id"], ["id"],
    )
    op.drop_constraint("messages_session_id_fkey", "messages", type_="foreignkey")
    op.create_foreign_key(
        "messages_session_id_fkey", "messages", "sessions",
        ["session_id"], ["id"],
    )
