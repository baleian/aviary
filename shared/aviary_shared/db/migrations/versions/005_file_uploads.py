"""File uploads table for storing user-uploaded images

Revision ID: 005
Revises: 004
Create Date: 2026-04-12
"""
from typing import Sequence, Union

import sqlalchemy as sa
from alembic import op
from sqlalchemy.dialects.postgresql import UUID, TIMESTAMP

revision: str = "005"
down_revision: Union[str, None] = "004"
branch_labels: Union[str, Sequence[str], None] = None
depends_on: Union[str, Sequence[str], None] = None


def upgrade() -> None:
    op.create_table(
        "file_uploads",
        sa.Column("id", UUID(as_uuid=True), primary_key=True, server_default=sa.text("gen_random_uuid()")),
        sa.Column("user_id", UUID(as_uuid=True), sa.ForeignKey("users.id", ondelete="CASCADE"), nullable=False),
        sa.Column("content_type", sa.String(100), nullable=False),
        sa.Column("filename", sa.String(255), nullable=False),
        sa.Column("data", sa.LargeBinary, nullable=False),
        sa.Column("size_bytes", sa.Integer, nullable=False),
        sa.Column("created_at", TIMESTAMP(timezone=True), server_default=sa.text("now()")),
    )
    op.create_index("idx_file_uploads_user", "file_uploads", ["user_id"])


def downgrade() -> None:
    op.drop_index("idx_file_uploads_user", table_name="file_uploads")
    op.drop_table("file_uploads")
