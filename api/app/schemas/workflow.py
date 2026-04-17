from datetime import datetime

from pydantic import BaseModel, Field

from app.schemas.agent import ModelConfig


class WorkflowCreate(BaseModel):
    name: str = Field(..., min_length=1, max_length=255)
    slug: str = Field(..., min_length=1, max_length=255, pattern="^[a-z0-9][a-z0-9-]*[a-z0-9]$")
    description: str | None = None
    model_config_data: ModelConfig = Field(..., alias="model_config")

    model_config = {"populate_by_name": True}


class WorkflowUpdate(BaseModel):
    name: str | None = Field(None, min_length=1, max_length=255)
    description: str | None = None
    definition: dict | None = None
    model_config_data: ModelConfig | None = Field(None, alias="model_config")

    model_config = {"populate_by_name": True}


class WorkflowResponse(BaseModel):
    model_config = {"from_attributes": True, "populate_by_name": True}

    id: str
    name: str
    slug: str
    description: str | None = None
    owner_id: str
    definition: dict
    model_config_data: dict = Field(alias="model_config")
    status: str
    current_version: int | None = None
    created_at: datetime
    updated_at: datetime

    @classmethod
    def from_orm_workflow(cls, workflow, current_version: int | None = None) -> "WorkflowResponse":
        return cls(
            id=str(workflow.id),
            name=workflow.name,
            slug=workflow.slug,
            description=workflow.description,
            owner_id=str(workflow.owner_id),
            definition=workflow.definition,
            model_config=workflow.model_config_json or {},
            status=workflow.status,
            current_version=current_version,
            created_at=workflow.created_at,
            updated_at=workflow.updated_at,
        )


class WorkflowListResponse(BaseModel):
    items: list[WorkflowResponse]
    total: int


class WorkflowVersionResponse(BaseModel):
    model_config = {"from_attributes": True}

    id: str
    workflow_id: str
    version: int
    deployed_by: str
    deployed_at: datetime

    @classmethod
    def from_orm_version(cls, v) -> "WorkflowVersionResponse":
        return cls(
            id=str(v.id),
            workflow_id=str(v.workflow_id),
            version=v.version,
            deployed_by=str(v.deployed_by),
            deployed_at=v.deployed_at,
        )
