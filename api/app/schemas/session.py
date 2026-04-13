import uuid
from datetime import datetime

from pydantic import BaseModel, ConfigDict


class SessionCreate(BaseModel):
    agent_id: uuid.UUID
    title: str | None = None


class SessionResponse(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    id: uuid.UUID
    agent_id: uuid.UUID
    created_by: str
    title: str | None
    status: str
    last_message_at: datetime | None
    created_at: datetime


class SessionListResponse(BaseModel):
    items: list[SessionResponse]


class SessionTitleUpdate(BaseModel):
    title: str
