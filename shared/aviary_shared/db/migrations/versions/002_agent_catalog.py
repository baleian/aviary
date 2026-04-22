"""Agent Catalog — catalog_agents, agent_versions, catalog_imports; agent linkage.

Revision ID: 002
Revises: 001
Create Date: 2026-04-22
"""
from typing import Sequence, Union

import sqlalchemy as sa
from alembic import op
from sqlalchemy.dialects import postgresql

revision: str = "002"
down_revision: Union[str, None] = "001"
branch_labels: Union[str, Sequence[str], None] = None
depends_on: Union[str, Sequence[str], None] = None


def upgrade() -> None:
    # pg_trgm is used for trigram-accelerated ILIKE on version name/description.
    op.execute("CREATE EXTENSION IF NOT EXISTS pg_trgm")

    # -------------------------------------------------------------------------
    # catalog_agents — independent catalog entity. Circular FK to agent_versions
    # resolved with use_alter=True; the FK is added after agent_versions exists.
    # -------------------------------------------------------------------------
    op.create_table(
        "catalog_agents",
        sa.Column(
            "id",
            postgresql.UUID(as_uuid=True),
            primary_key=True,
            server_default=sa.text("gen_random_uuid()"),
        ),
        sa.Column(
            "owner_id",
            postgresql.UUID(as_uuid=True),
            sa.ForeignKey("users.id", ondelete="RESTRICT"),
            nullable=False,
        ),
        sa.Column("slug", sa.String(255), nullable=False),
        sa.Column("current_version_id", postgresql.UUID(as_uuid=True), nullable=True),
        sa.Column("is_published", sa.Boolean, nullable=False, server_default=sa.text("true")),
        sa.Column("category", sa.String(32), nullable=False),
        sa.Column("unpublished_at", sa.TIMESTAMP(timezone=True), nullable=True),
        sa.Column(
            "created_at",
            sa.TIMESTAMP(timezone=True),
            server_default=sa.text("now()"),
            nullable=False,
        ),
        sa.Column(
            "updated_at",
            sa.TIMESTAMP(timezone=True),
            server_default=sa.text("now()"),
            nullable=False,
        ),
    )
    op.create_index(
        "idx_catalog_agents_published_category",
        "catalog_agents",
        ["is_published", "category"],
    )
    op.create_index("idx_catalog_agents_owner", "catalog_agents", ["owner_id"])

    # -------------------------------------------------------------------------
    # agent_versions — immutable snapshot of a catalog agent at publish time.
    # -------------------------------------------------------------------------
    op.create_table(
        "agent_versions",
        sa.Column(
            "id",
            postgresql.UUID(as_uuid=True),
            primary_key=True,
            server_default=sa.text("gen_random_uuid()"),
        ),
        sa.Column(
            "catalog_agent_id",
            postgresql.UUID(as_uuid=True),
            sa.ForeignKey("catalog_agents.id", ondelete="CASCADE"),
            nullable=False,
        ),
        sa.Column("version_number", sa.Integer, nullable=False),
        sa.Column("name", sa.String(255), nullable=False),
        sa.Column("description", sa.Text, nullable=True),
        sa.Column("icon", sa.String(50), nullable=True),
        sa.Column("instruction", sa.Text, nullable=False),
        sa.Column(
            "model_config",
            postgresql.JSONB,
            nullable=False,
            server_default=sa.text("'{}'::jsonb"),
        ),
        sa.Column(
            "tools",
            postgresql.JSONB,
            nullable=False,
            server_default=sa.text("'[]'::jsonb"),
        ),
        sa.Column(
            "mcp_servers",
            postgresql.JSONB,
            nullable=False,
            server_default=sa.text("'[]'::jsonb"),
        ),
        sa.Column(
            "mcp_tool_bindings",
            postgresql.JSONB,
            nullable=False,
            server_default=sa.text("'[]'::jsonb"),
        ),
        sa.Column("category", sa.String(32), nullable=False),
        sa.Column("release_notes", sa.Text, nullable=True),
        sa.Column(
            "published_by",
            postgresql.UUID(as_uuid=True),
            sa.ForeignKey("users.id"),
            nullable=False,
        ),
        sa.Column(
            "published_at",
            sa.TIMESTAMP(timezone=True),
            server_default=sa.text("now()"),
            nullable=False,
        ),
        sa.Column("unpublished_at", sa.TIMESTAMP(timezone=True), nullable=True),
        sa.UniqueConstraint(
            "catalog_agent_id", "version_number", name="uq_agent_versions_num"
        ),
    )
    op.create_index(
        "idx_agent_versions_published",
        "agent_versions",
        ["catalog_agent_id", sa.text("published_at DESC")],
    )
    # GIN indices for filter/search.
    op.execute(
        "CREATE INDEX idx_agent_versions_bindings_gin "
        "ON agent_versions USING GIN (mcp_tool_bindings jsonb_path_ops)"
    )
    op.execute(
        "CREATE INDEX idx_agent_versions_name_trgm "
        "ON agent_versions USING GIN (name gin_trgm_ops)"
    )
    op.execute(
        "CREATE INDEX idx_agent_versions_description_trgm "
        "ON agent_versions USING GIN (description gin_trgm_ops)"
    )

    # Add the circular FK catalog_agents.current_version_id -> agent_versions.id
    op.create_foreign_key(
        "fk_catalog_agents_current_version",
        "catalog_agents",
        "agent_versions",
        ["current_version_id"],
        ["id"],
        use_alter=True,
    )

    # -------------------------------------------------------------------------
    # catalog_imports — thin subscription metadata, 1:1 with an agents row.
    # -------------------------------------------------------------------------
    op.create_table(
        "catalog_imports",
        sa.Column(
            "id",
            postgresql.UUID(as_uuid=True),
            primary_key=True,
            server_default=sa.text("gen_random_uuid()"),
        ),
        sa.Column(
            "catalog_agent_id",
            postgresql.UUID(as_uuid=True),
            sa.ForeignKey("catalog_agents.id", ondelete="RESTRICT"),
            nullable=False,
        ),
        sa.Column(
            "pinned_version_id",
            postgresql.UUID(as_uuid=True),
            sa.ForeignKey("agent_versions.id"),
            nullable=True,
        ),
        sa.Column(
            "imported_at",
            sa.TIMESTAMP(timezone=True),
            server_default=sa.text("now()"),
            nullable=False,
        ),
        sa.Column(
            "updated_at",
            sa.TIMESTAMP(timezone=True),
            server_default=sa.text("now()"),
            nullable=False,
        ),
    )

    # -------------------------------------------------------------------------
    # agents — add catalog linkage columns, drop global slug UNIQUE, add
    # (owner_id, slug) UNIQUE.
    # -------------------------------------------------------------------------
    op.add_column(
        "agents",
        sa.Column(
            "linked_catalog_agent_id",
            postgresql.UUID(as_uuid=True),
            sa.ForeignKey("catalog_agents.id", ondelete="SET NULL"),
            nullable=True,
        ),
    )
    op.add_column(
        "agents",
        sa.Column(
            "catalog_import_id",
            postgresql.UUID(as_uuid=True),
            sa.ForeignKey("catalog_imports.id", ondelete="SET NULL"),
            nullable=True,
        ),
    )
    # Drop global slug unique. In the baseline it was declared as
    # `unique=True` which Postgres materializes as a UNIQUE INDEX named
    # `agents_slug_key`. Drop that index/constraint safely regardless of name.
    op.execute("ALTER TABLE agents DROP CONSTRAINT IF EXISTS agents_slug_key")
    op.execute("DROP INDEX IF EXISTS agents_slug_key")
    op.create_unique_constraint("uq_agents_owner_slug", "agents", ["owner_id", "slug"])


def downgrade() -> None:
    op.drop_constraint("uq_agents_owner_slug", "agents", type_="unique")
    op.create_unique_constraint("agents_slug_key", "agents", ["slug"])
    op.drop_column("agents", "catalog_import_id")
    op.drop_column("agents", "linked_catalog_agent_id")

    op.drop_table("catalog_imports")

    op.drop_constraint(
        "fk_catalog_agents_current_version", "catalog_agents", type_="foreignkey"
    )
    op.execute("DROP INDEX IF EXISTS idx_agent_versions_description_trgm")
    op.execute("DROP INDEX IF EXISTS idx_agent_versions_name_trgm")
    op.execute("DROP INDEX IF EXISTS idx_agent_versions_bindings_gin")
    op.drop_index("idx_agent_versions_published", table_name="agent_versions")
    op.drop_table("agent_versions")
    op.drop_index("idx_catalog_agents_owner", table_name="catalog_agents")
    op.drop_index(
        "idx_catalog_agents_published_category", table_name="catalog_agents"
    )
    op.drop_table("catalog_agents")
