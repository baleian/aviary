"""Catalog FK hardening: SET NULL on version delete + FK indexes.

Revision ID: 003
Revises: 002
Create Date: 2026-04-22
"""
from typing import Sequence, Union

import sqlalchemy as sa
from alembic import op

revision: str = "003"
down_revision: Union[str, None] = "002"
branch_labels: Union[str, Sequence[str], None] = None
depends_on: Union[str, Sequence[str], None] = None


def upgrade() -> None:
    # A retracted or deleted AgentVersion should not break the catalog row
    # (current_version_id) or break active imports (pinned_version_id).
    # Fall back to NULL (= latest / no-current), which both runtime resolve
    # paths already handle.
    op.drop_constraint(
        "fk_catalog_agents_current_version", "catalog_agents", type_="foreignkey"
    )
    op.create_foreign_key(
        "fk_catalog_agents_current_version",
        "catalog_agents",
        "agent_versions",
        ["current_version_id"],
        ["id"],
        ondelete="SET NULL",
        use_alter=True,
    )

    op.drop_constraint(
        "catalog_imports_pinned_version_id_fkey",
        "catalog_imports",
        type_="foreignkey",
    )
    op.create_foreign_key(
        "catalog_imports_pinned_version_id_fkey",
        "catalog_imports",
        "agent_versions",
        ["pinned_version_id"],
        ["id"],
        ondelete="SET NULL",
    )

    # FK columns queried on every catalog list + publisher mine view.
    op.create_index(
        "idx_agents_catalog_import_id",
        "agents",
        ["catalog_import_id"],
        postgresql_where=sa.text("catalog_import_id IS NOT NULL"),
    )
    op.create_index(
        "idx_agents_linked_catalog_agent_id",
        "agents",
        ["linked_catalog_agent_id"],
        postgresql_where=sa.text("linked_catalog_agent_id IS NOT NULL"),
    )
    op.create_index(
        "idx_catalog_imports_catalog_agent_id",
        "catalog_imports",
        ["catalog_agent_id"],
    )


def downgrade() -> None:
    op.drop_index("idx_catalog_imports_catalog_agent_id", table_name="catalog_imports")
    op.drop_index("idx_agents_linked_catalog_agent_id", table_name="agents")
    op.drop_index("idx_agents_catalog_import_id", table_name="agents")

    op.drop_constraint(
        "catalog_imports_pinned_version_id_fkey",
        "catalog_imports",
        type_="foreignkey",
    )
    op.create_foreign_key(
        "catalog_imports_pinned_version_id_fkey",
        "catalog_imports",
        "agent_versions",
        ["pinned_version_id"],
        ["id"],
    )

    op.drop_constraint(
        "fk_catalog_agents_current_version", "catalog_agents", type_="foreignkey"
    )
    op.create_foreign_key(
        "fk_catalog_agents_current_version",
        "catalog_agents",
        "agent_versions",
        ["current_version_id"],
        ["id"],
        use_alter=True,
    )
