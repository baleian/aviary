"""Phase 2: flip policy↔agent FK so agents own their policy 1:1.

Before: `agents.policy_id → policies.id` (nullable, agent points at policy).
After:  `policies.agent_id → agents.id` ON DELETE CASCADE, unique.

With this, agent hard-delete automatically removes the policy row via DB
cascade — no explicit cleanup in the service layer.

Revision ID: 004
Revises: 003
Create Date: 2026-04-14
"""
from typing import Sequence, Union

from alembic import op
import sqlalchemy as sa
from sqlalchemy.dialects import postgresql

revision: str = "004"
down_revision: Union[str, None] = "003"
branch_labels: Union[str, Sequence[str], None] = None
depends_on: Union[str, Sequence[str], None] = None


def upgrade() -> None:
    op.add_column(
        "policies",
        sa.Column("agent_id", postgresql.UUID(as_uuid=True), nullable=True),
    )
    op.execute(
        "UPDATE policies p SET agent_id = a.id FROM agents a WHERE a.policy_id = p.id"
    )
    # Any policy row no longer linked to an agent (shouldn't happen in Phase 1
    # dev data, but defensive) is removed rather than left dangling.
    op.execute("DELETE FROM policies WHERE agent_id IS NULL")
    op.alter_column("policies", "agent_id", nullable=False)
    op.create_unique_constraint("policies_agent_id_key", "policies", ["agent_id"])
    op.create_foreign_key(
        "policies_agent_id_fkey", "policies", "agents",
        ["agent_id"], ["id"], ondelete="CASCADE",
    )

    op.drop_constraint("agents_policy_id_fkey", "agents", type_="foreignkey")
    op.drop_column("agents", "policy_id")


def downgrade() -> None:
    op.add_column(
        "agents",
        sa.Column("policy_id", postgresql.UUID(as_uuid=True), nullable=True),
    )
    op.create_foreign_key(
        "agents_policy_id_fkey", "agents", "policies", ["policy_id"], ["id"],
    )
    op.execute(
        "UPDATE agents a SET policy_id = p.id FROM policies p WHERE p.agent_id = a.id"
    )
    op.drop_constraint("policies_agent_id_fkey", "policies", type_="foreignkey")
    op.drop_constraint("policies_agent_id_key", "policies", type_="unique")
    op.drop_column("policies", "agent_id")
