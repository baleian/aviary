from datetime import datetime

from pydantic import BaseModel, Field

from app.schemas.agent import ModelConfig


class WorkflowCreate(BaseModel):
    name: str = Field(..., min_length=1, max_length=255)
    slug: str = Field(..., min_length=1, max_length=255, pattern="^[a-z0-9][a-z0-9-]*[a-z0-9]$")
    description: str | None = None
    model_config_data: ModelConfig = Field(..., alias="model_config")
    visibility: str = Field("private", pattern="^(public|team|private)$")

    model_config = {"populate_by_name": True}


class WorkflowUpdate(BaseModel):
    name: str | None = Field(None, min_length=1, max_length=255)
    description: str | None = None
    definition: dict | None = None
    model_config_data: ModelConfig | None = Field(None, alias="model_config")
    visibility: str | None = Field(None, pattern="^(public|team|private)$")

    model_config = {"populate_by_name": True}


class WorkflowResponse(BaseModel):
    model_config = {"from_attributes": True, "populate_by_name": True}

    id: str
    name: str
    slug: str
    description: str | None = None
    owner_id: str
    visibility: str
    definition: dict
    model_config_data: dict = Field(alias="model_config")
    status: str
    created_at: datetime
    updated_at: datetime

    @classmethod
    def from_orm_workflow(cls, workflow) -> "WorkflowResponse":
        return cls(
            id=str(workflow.id),
            name=workflow.name,
            slug=workflow.slug,
            description=workflow.description,
            owner_id=str(workflow.owner_id),
            visibility=workflow.visibility,
            definition=workflow.definition,
            model_config=workflow.model_config_json or {},
            status=workflow.status,
            created_at=workflow.created_at,
            updated_at=workflow.updated_at,
        )


class WorkflowListResponse(BaseModel):
    items: list[WorkflowResponse]
    total: int


class WorkflowRunCreate(BaseModel):
    trigger_type: str = "manual"
    trigger_data: dict = Field(default_factory=dict)


class WorkflowNodeRunResponse(BaseModel):
    model_config = {"from_attributes": True}

    id: str
    node_id: str
    node_type: str
    status: str
    input_data: dict | None = None
    output_data: dict | None = None
    started_at: datetime | None = None
    completed_at: datetime | None = None
    error: str | None = None

    @classmethod
    def from_orm(cls, node_run) -> "WorkflowNodeRunResponse":
        return cls(
            id=str(node_run.id),
            node_id=node_run.node_id,
            node_type=node_run.node_type,
            status=node_run.status,
            input_data=node_run.input_data,
            output_data=node_run.output_data,
            started_at=node_run.started_at,
            completed_at=node_run.completed_at,
            error=node_run.error,
        )


class WorkflowRunResponse(BaseModel):
    model_config = {"from_attributes": True}

    id: str
    workflow_id: str
    triggered_by: str
    trigger_type: str
    status: str
    started_at: datetime | None = None
    completed_at: datetime | None = None
    error: str | None = None
    created_at: datetime
    node_runs: list[WorkflowNodeRunResponse] = []

    @classmethod
    def from_orm_run(cls, run, include_node_runs: bool = False) -> "WorkflowRunResponse":
        node_runs = []
        if include_node_runs:
            node_runs = [WorkflowNodeRunResponse.from_orm(nr) for nr in run.node_runs]
        return cls(
            id=str(run.id),
            workflow_id=str(run.workflow_id),
            triggered_by=str(run.triggered_by),
            trigger_type=run.trigger_type,
            status=run.status,
            started_at=run.started_at,
            completed_at=run.completed_at,
            error=run.error,
            created_at=run.created_at,
            node_runs=node_runs,
        )


class WorkflowRunListResponse(BaseModel):
    items: list[WorkflowRunResponse]
    total: int


class WorkflowVersionResponse(BaseModel):
    id: str
    version: int
    deployed_by: str
    deployed_at: datetime
