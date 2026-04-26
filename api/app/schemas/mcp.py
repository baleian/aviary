"""MCP catalog and binding schemas. Tool ids on the wire are the
gateway's qualified ``{server}__{tool}`` form."""

from pydantic import BaseModel


class McpServerResponse(BaseModel):
    id: str
    name: str
    description: str | None
    tags: list[str]
    tool_count: int


class McpToolResponse(BaseModel):
    id: str  # qualified_name — stable identifier used in bindings
    server_id: str
    server_name: str
    name: str  # unprefixed tool name
    description: str | None
    input_schema: dict
    qualified_name: str  # "{server}__{tool}" — mirrors `id`


class McpToolBindRequest(BaseModel):
    # Frontend sends `qualified_name` strings (e.g. "jira__get_issue").
    tool_ids: list[str]


class McpToolBindingResponse(BaseModel):
    id: str
    agent_id: str
    tool: McpToolResponse
