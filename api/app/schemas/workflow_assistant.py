from typing import Annotated, Literal, Union

from pydantic import BaseModel, Field

NodeTypeLiteral = Literal[
    "manual_trigger",
    "webhook_trigger",
    "agent_step",
    "condition",
    "merge",
    "payload_parser",
    "template",
]


class Position(BaseModel):
    x: float
    y: float


class AddNodeOp(BaseModel):
    op: Literal["add_node"]
    id: str = Field(..., min_length=1)
    type: NodeTypeLiteral
    position: Position
    data: dict


class UpdateNodeOp(BaseModel):
    op: Literal["update_node"]
    id: str = Field(..., min_length=1)
    data_patch: dict


class DeleteNodeOp(BaseModel):
    op: Literal["delete_node"]
    id: str = Field(..., min_length=1)


class AddEdgeOp(BaseModel):
    model_config = {"populate_by_name": True}

    op: Literal["add_edge"]
    id: str | None = None
    source: str = Field(..., min_length=1)
    target: str = Field(..., min_length=1)
    source_handle: str | None = Field(None, alias="sourceHandle")
    target_handle: str | None = Field(None, alias="targetHandle")


class DeleteEdgeOp(BaseModel):
    op: Literal["delete_edge"]
    id: str = Field(..., min_length=1)


PlanOp = Annotated[
    Union[AddNodeOp, UpdateNodeOp, DeleteNodeOp, AddEdgeOp, DeleteEdgeOp],
    Field(discriminator="op"),
]


class AssistantTurn(BaseModel):
    role: Literal["user", "assistant"]
    content: str


class WorkflowAssistantRequest(BaseModel):
    user_message: str = Field(..., min_length=1)
    current_definition: dict
    history: list[AssistantTurn] = Field(default_factory=list)


class WorkflowAssistantResponse(BaseModel):
    reply: str
    plan: list[PlanOp]
