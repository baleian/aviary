import uuid
from datetime import datetime

from pydantic import BaseModel, ConfigDict, Field


class AgentCreate(BaseModel):
    model_config = ConfigDict(protected_namespaces=(), populate_by_name=True)

    name: str = Field(min_length=1, max_length=255)
    slug: str = Field(min_length=1, max_length=255, pattern=r"^[a-z0-9][a-z0-9-]*$")
    instruction: str | None = None
    model_config_data: dict | None = Field(default=None, alias="model_config")
    tools: dict | None = None


class AgentUpdate(BaseModel):
    model_config = ConfigDict(protected_namespaces=(), populate_by_name=True)

    name: str | None = Field(default=None, min_length=1, max_length=255)
    instruction: str | None = None
    model_config_data: dict | None = Field(default=None, alias="model_config")
    tools: dict | None = None


class AgentResponse(BaseModel):
    model_config = ConfigDict(
        from_attributes=True, populate_by_name=True, protected_namespaces=()
    )

    id: uuid.UUID
    name: str
    slug: str
    owner_id: str
    instruction: str | None = None
    model_config_data: dict | None = Field(default=None, serialization_alias="model_config")
    tools: dict | None = None
    status: str
    created_at: datetime
    updated_at: datetime


class AgentListResponse(BaseModel):
    items: list[AgentResponse]
