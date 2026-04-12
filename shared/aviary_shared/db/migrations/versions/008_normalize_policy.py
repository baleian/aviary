"""Normalize policy into shared entity, make workflows independent

Revision ID: 008
Revises: 007
Create Date: 2026-04-12
"""
from typing import Sequence, Union

import sqlalchemy as sa
from alembic import op
from sqlalchemy.dialects.postgresql import JSONB, UUID

revision: str = "008"
down_revision: Union[str, None] = "007"
branch_labels: Union[str, Sequence[str], None] = None
depends_on: Union[str, Sequence[str], None] = None

EMPTY_DEFINITION = '{"nodes":[],"edges":[],"viewport":{"x":0,"y":0,"zoom":1}}'


def upgrade() -> None:
    # 1. Create policies table
    op.create_table(
        "policies",
        sa.Column("id", UUID(as_uuid=True), server_default=sa.text("gen_random_uuid()"), primary_key=True),
        sa.Column("pod_strategy", sa.String(20), server_default="lazy", nullable=False),
        sa.Column("min_pods", sa.Integer, server_default="1", nullable=False),
        sa.Column("max_pods", sa.Integer, server_default="3", nullable=False),
        sa.Column("policy_rules", JSONB, server_default="{}", nullable=False),
        sa.Column("resource_limits", JSONB, server_default="{}", nullable=False),
        sa.Column("last_activity_at", sa.TIMESTAMP(timezone=True), nullable=True),
        sa.Column("created_at", sa.TIMESTAMP(timezone=True), server_default=sa.text("now()")),
        sa.Column("updated_at", sa.TIMESTAMP(timezone=True), server_default=sa.text("now()")),
    )

    # 2. Modify agents: drop infra fields, add policy_id
    op.drop_column("agents", "policy")
    op.drop_column("agents", "pod_strategy")
    op.drop_column("agents", "min_pods")
    op.drop_column("agents", "max_pods")
    op.drop_column("agents", "last_activity_at")
    op.add_column("agents", sa.Column(
        "policy_id", UUID(as_uuid=True),
        sa.ForeignKey("policies.id", ondelete="SET NULL"), nullable=True,
    ))

    # 3. Drop all workflow-related tables
    op.drop_table("workflow_node_runs")
    op.drop_table("workflow_runs")
    op.drop_table("workflow_versions")
    op.drop_table("workflow_acl")
    op.drop_table("workflows")

    # 4. Recreate workflows as independent entity
    op.create_table(
        "workflows",
        sa.Column("id", UUID(as_uuid=True), server_default=sa.text("gen_random_uuid()"), primary_key=True),
        sa.Column("name", sa.String(255), nullable=False),
        sa.Column("slug", sa.String(255), unique=True, nullable=False),
        sa.Column("description", sa.Text, nullable=True),
        sa.Column("owner_id", UUID(as_uuid=True), sa.ForeignKey("users.id"), nullable=False),
        sa.Column("visibility", sa.String(20), server_default="private", nullable=False),
        sa.Column("model_config", JSONB, server_default="{}", nullable=False),
        sa.Column("policy_id", UUID(as_uuid=True), sa.ForeignKey("policies.id", ondelete="SET NULL"), nullable=True),
        sa.Column("definition", JSONB, server_default=EMPTY_DEFINITION, nullable=False),
        sa.Column("status", sa.String(20), server_default="draft", nullable=False),
        sa.Column("created_at", sa.TIMESTAMP(timezone=True), server_default=sa.text("now()")),
        sa.Column("updated_at", sa.TIMESTAMP(timezone=True), server_default=sa.text("now()")),
    )

    # 5. Recreate workflow support tables
    op.create_table(
        "workflow_acl",
        sa.Column("id", UUID(as_uuid=True), server_default=sa.text("gen_random_uuid()"), primary_key=True),
        sa.Column("workflow_id", UUID(as_uuid=True), sa.ForeignKey("workflows.id", ondelete="CASCADE"), nullable=False),
        sa.Column("user_id", UUID(as_uuid=True), sa.ForeignKey("users.id", ondelete="CASCADE"), nullable=True),
        sa.Column("team_id", UUID(as_uuid=True), sa.ForeignKey("teams.id", ondelete="CASCADE"), nullable=True),
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
        sa.Column("workflow_id", UUID(as_uuid=True), sa.ForeignKey("workflows.id", ondelete="CASCADE"), nullable=False),
        sa.Column("triggered_by", UUID(as_uuid=True), sa.ForeignKey("users.id"), nullable=False),
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
        sa.Column("run_id", UUID(as_uuid=True), sa.ForeignKey("workflow_runs.id", ondelete="CASCADE"), nullable=False),
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

    op.create_table(
        "workflow_versions",
        sa.Column("id", UUID(as_uuid=True), server_default=sa.text("gen_random_uuid()"), primary_key=True),
        sa.Column("workflow_id", UUID(as_uuid=True), sa.ForeignKey("workflows.id", ondelete="CASCADE"), nullable=False),
        sa.Column("version", sa.Integer, nullable=False),
        sa.Column("definition_snapshot", JSONB, nullable=False),
        sa.Column("deployed_by", UUID(as_uuid=True), sa.ForeignKey("users.id"), nullable=False),
        sa.Column("deployed_at", sa.TIMESTAMP(timezone=True), server_default=sa.text("now()")),
        sa.UniqueConstraint("workflow_id", "version", name="uq_workflow_version"),
    )


def downgrade() -> None:
    op.drop_table("workflow_versions")
    op.drop_table("workflow_node_runs")
    op.drop_table("workflow_runs")
    op.drop_table("workflow_acl")
    op.drop_table("workflows")
    op.drop_column("agents", "policy_id")
    op.add_column("agents", sa.Column("policy", JSONB, server_default="{}", nullable=False))
    op.add_column("agents", sa.Column("pod_strategy", sa.String(20), server_default="lazy"))
    op.add_column("agents", sa.Column("min_pods", sa.Integer, server_default="1"))
    op.add_column("agents", sa.Column("max_pods", sa.Integer, server_default="3"))
    op.add_column("agents", sa.Column("last_activity_at", sa.TIMESTAMP(timezone=True), nullable=True))
    op.drop_table("policies")
