import uuid
from datetime import datetime

from pydantic import BaseModel, ConfigDict, Field


class ModelConfig(BaseModel):
    """LiteLLM routing config — opaque to the API, validated at request time."""

    backend: str = Field(min_length=1)
    model: str = Field(min_length=1)
    max_output_tokens: int | None = None


_AGENT_CONFIG = ConfigDict(protected_namespaces=(), populate_by_name=True)


class AgentCreate(BaseModel):
    model_config = _AGENT_CONFIG

    name: str = Field(min_length=1, max_length=255)
    slug: str = Field(min_length=1, max_length=255, pattern=r"^[a-z0-9][a-z0-9-]*$")
    description: str | None = None
    instruction: str | None = None
    model_config_data: ModelConfig = Field(alias="model_config")
    tools: list[str] | None = None


class AgentUpdate(BaseModel):
    model_config = _AGENT_CONFIG

    name: str | None = Field(default=None, min_length=1, max_length=255)
    description: str | None = None
    instruction: str | None = None
    model_config_data: ModelConfig | None = Field(default=None, alias="model_config")
    tools: list[str] | None = None


class AgentResponse(BaseModel):
    model_config = ConfigDict(
        from_attributes=True, populate_by_name=True, protected_namespaces=()
    )

    id: uuid.UUID
    name: str
    slug: str
    owner_id: str
    description: str | None = None
    instruction: str | None = None
    model_config_data: ModelConfig | None = Field(default=None, alias="model_config")
    tools: list[str] | None = None
    status: str
    created_at: datetime
    updated_at: datetime


class AgentListResponse(BaseModel):
    items: list[AgentResponse]
