from pydantic import BaseModel


class McpServerResponse(BaseModel):
    id: str
    name: str
    description: str | None
    tags: list[str]
    tool_count: int


class McpToolResponse(BaseModel):
    id: str
    server_id: str
    server_name: str
    name: str
    description: str | None
    input_schema: dict
    qualified_name: str


class McpToolBindingResponse(BaseModel):
    id: str
    agent_id: str
    tool: McpToolResponse


class McpToolBindRequest(BaseModel):
    tool_ids: list[str]
