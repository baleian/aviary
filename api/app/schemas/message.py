import uuid
from datetime import datetime

from pydantic import BaseModel, ConfigDict, Field


class MessageResponse(BaseModel):
    model_config = ConfigDict(from_attributes=True, populate_by_name=True)

    id: uuid.UUID
    session_id: uuid.UUID
    sender_type: str
    sender_id: str | None
    content: str | None
    metadata_: dict | None = Field(default=None, alias="metadata", serialization_alias="metadata")
    created_at: datetime


class MessagePageResponse(BaseModel):
    messages: list[MessageResponse]
    has_more: bool
