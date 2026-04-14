"""Workflow tables

Revision ID: 006
Revises: 005
Create Date: 2026-04-12
"""
from typing import Sequence, Union

import sqlalchemy as sa
from alembic import op
from sqlalchemy.dialects.postgresql import JSONB, UUID

revision: str = "006"
down_revision: Union[str, None] = "005"
branch_labels: Union[str, Sequence[str], None] = None
depends_on: Union[str, Sequence[str], None] = None

EMPTY_DEFINITION = '{"nodes":[],"edges":[],"viewport":{"x":0,"y":0,"zoom":1}}'


def upgrade() -> None:
    op.create_table(
        "workflows",
        sa.Column("id", UUID(as_uuid=True), server_default=sa.text("gen_random_uuid()"), primary_key=True),
        sa.Column("name", sa.String(255), nullable=False),
        sa.Column("slug", sa.String(255), unique=True, nullable=False),
        sa.Column("description", sa.Text, nullable=True),
        sa.Column(
            "owner_id", UUID(as_uuid=True),
            sa.ForeignKey("users.id"), nullable=False,
        ),
        sa.Column("visibility", sa.String(20), server_default="private", nullable=False),
        sa.Column("definition", JSONB, server_default=EMPTY_DEFINITION, nullable=False),
        sa.Column(
            "worker_agent_id", UUID(as_uuid=True),
            sa.ForeignKey("agents.id", ondelete="SET NULL"), nullable=True,
        ),
        sa.Column("status", sa.String(20), server_default="draft", nullable=False),
        sa.Column("created_at", sa.TIMESTAMP(timezone=True), server_default=sa.text("now()")),
        sa.Column("updated_at", sa.TIMESTAMP(timezone=True), server_default=sa.text("now()")),
    )

    op.create_table(
        "workflow_acl",
        sa.Column("id", UUID(as_uuid=True), server_default=sa.text("gen_random_uuid()"), primary_key=True),
        sa.Column(
            "workflow_id", UUID(as_uuid=True),
            sa.ForeignKey("workflows.id", ondelete="CASCADE"), nullable=False,
        ),
        sa.Column(
            "user_id", UUID(as_uuid=True),
            sa.ForeignKey("users.id", ondelete="CASCADE"), nullable=True,
        ),
        sa.Column(
            "team_id", UUID(as_uuid=True),
            sa.ForeignKey("teams.id", ondelete="CASCADE"), nullable=True,
        ),
        sa.Column("role", sa.String(20), nullable=False),
        sa.Column("created_at", sa.TIMESTAMP(timezone=True), server_default=sa.text("now()")),
        sa.CheckConstraint(
            "(user_id IS NOT NULL AND team_id IS NULL) OR (user_id IS NULL AND team_id IS NOT NULL)",
            name="workflow_acl_grantee",
        ),
        sa.UniqueConstraint("workflow_id", "user_id", name="uq_workflow_acl_user"),
        sa.UniqueConstraint("workflow_id", "team_id", name="uq_workflow_acl_team"),
    )

    op.create_table(
        "workflow_runs",
        sa.Column("id", UUID(as_uuid=True), server_default=sa.text("gen_random_uuid()"), primary_key=True),
        sa.Column(
            "workflow_id", UUID(as_uuid=True),
            sa.ForeignKey("workflows.id", ondelete="CASCADE"), nullable=False,
        ),
        sa.Column(
            "triggered_by", UUID(as_uuid=True),
            sa.ForeignKey("users.id"), nullable=False,
        ),
        sa.Column("trigger_type", sa.String(20), nullable=False),
        sa.Column("trigger_data", JSONB, server_default="{}", nullable=False),
        sa.Column("definition_snapshot", JSONB, nullable=False),
        sa.Column("status", sa.String(20), server_default="pending", nullable=False),
        sa.Column("started_at", sa.TIMESTAMP(timezone=True), nullable=True),
        sa.Column("completed_at", sa.TIMESTAMP(timezone=True), nullable=True),
        sa.Column("error", sa.Text, nullable=True),
        sa.Column("created_at", sa.TIMESTAMP(timezone=True), server_default=sa.text("now()")),
    )

    op.create_table(
        "workflow_node_runs",
        sa.Column("id", UUID(as_uuid=True), server_default=sa.text("gen_random_uuid()"), primary_key=True),
        sa.Column(
            "run_id", UUID(as_uuid=True),
            sa.ForeignKey("workflow_runs.id", ondelete="CASCADE"), nullable=False,
        ),
        sa.Column("node_id", sa.String(100), nullable=False),
        sa.Column("node_type", sa.String(50), nullable=False),
        sa.Column("status", sa.String(20), server_default="pending", nullable=False),
        sa.Column("input_data", JSONB, nullable=True),
        sa.Column("output_data", JSONB, nullable=True),
        sa.Column("started_at", sa.TIMESTAMP(timezone=True), nullable=True),
        sa.Column("completed_at", sa.TIMESTAMP(timezone=True), nullable=True),
        sa.Column("error", sa.Text, nullable=True),
        sa.UniqueConstraint("run_id", "node_id", name="uq_workflow_node_run"),
    )


def downgrade() -> None:
    op.drop_table("workflow_node_runs")
    op.drop_table("workflow_runs")
    op.drop_table("workflow_acl")
    op.drop_table("workflows")
