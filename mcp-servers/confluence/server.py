"""Confluence MCP Server.

Standard FastMCP server for Confluence wiki operations.
The `confluence_token` argument on every tool is injected by the MCP Gateway.
"""

import json
from mcp.server.fastmcp import FastMCP

mcp = FastMCP("confluence", host="0.0.0.0", port=8000, stateless_http=True)


@mcp.tool()
async def get_page(
    confluence_token: str,
    page_id: str | None = None, space_key: str | None = None,
    title: str | None = None, expand: list[str] | None = None,
) -> str:
    """Get a Confluence page by ID or title."""
    # TODO: GET /rest/api/content/{page_id} or ?spaceKey=...&title=...
    return json.dumps({
        "id": page_id or "12345", "title": title or "Architecture Overview",
        "space": {"key": space_key or "ENG", "name": "Engineering"},
        "version": {"number": 5, "when": "2026-03-30T14:00:00Z"},
        "body": {"storage": {"value": "<h1>Architecture</h1><p>System architecture overview...</p>"}},
    })


@mcp.tool()
async def create_page(
    confluence_token: str, space_key: str, title: str, body: str,
    parent_id: str | None = None, content_format: str = "storage",
) -> str:
    """Create a new Confluence page."""
    # TODO: POST /rest/api/content
    return json.dumps({"id": "12346", "title": title, "space": {"key": space_key},
                        "_links": {"webui": f"/spaces/{space_key}/pages/12346/{title}"}})


@mcp.tool()
async def update_page(
    confluence_token: str, page_id: str, body: str,
    title: str | None = None, version_comment: str | None = None,
    content_format: str = "storage",
) -> str:
    """Update an existing Confluence page."""
    # TODO: PUT /rest/api/content/{page_id}
    return json.dumps({"id": page_id, "title": title or "Updated Page",
                        "version": {"number": 6, "when": "2026-04-05T10:00:00Z"}})


@mcp.tool()
async def delete_page(confluence_token: str, page_id: str) -> str:
    """Delete a Confluence page."""
    # TODO: DELETE /rest/api/content/{page_id}
    return f"Page {page_id} deleted successfully."


@mcp.tool()
async def search(confluence_token: str, cql: str, limit: int = 20) -> str:
    """Search Confluence content using CQL (Confluence Query Language)."""
    # TODO: GET /rest/api/content/search?cql={cql}
    return json.dumps({"totalSize": 3, "results": [
        {"id": "12345", "title": "Architecture Overview", "space": {"key": "ENG"}, "type": "page"},
        {"id": "12350", "title": "API Design Guide", "space": {"key": "ENG"}, "type": "page"},
        {"id": "12360", "title": "Auth Flow Diagram", "space": {"key": "SEC"}, "type": "page"},
    ]})


@mcp.tool()
async def get_child_pages(confluence_token: str, page_id: str, limit: int = 25) -> str:
    """Get child pages of a parent page."""
    # TODO: GET /rest/api/content/{page_id}/child/page
    return json.dumps({"results": [
        {"id": "12346", "title": "Backend Architecture"},
        {"id": "12347", "title": "Frontend Architecture"},
        {"id": "12348", "title": "Infrastructure"},
    ]})


@mcp.tool()
async def list_spaces(confluence_token: str, type: str | None = None, limit: int = 25) -> str:
    """List all Confluence spaces."""
    # TODO: GET /rest/api/space
    return json.dumps([
        {"key": "ENG", "name": "Engineering", "type": "global"},
        {"key": "SEC", "name": "Security", "type": "global"},
        {"key": "OPS", "name": "Operations", "type": "global"},
    ])


@mcp.tool()
async def get_space(confluence_token: str, space_key: str) -> str:
    """Get details of a Confluence space."""
    # TODO: GET /rest/api/space/{space_key}
    return json.dumps({"key": space_key, "name": "Engineering", "type": "global",
                        "description": "Engineering team documentation"})


@mcp.tool()
async def add_label(confluence_token: str, page_id: str, label: str) -> str:
    """Add a label to a Confluence page."""
    # TODO: POST /rest/api/content/{page_id}/label
    return f"Label '{label}' added to page {page_id}."


@mcp.tool()
async def get_page_history(confluence_token: str, page_id: str, limit: int = 10) -> str:
    """Get version history of a page."""
    # TODO: GET /rest/api/content/{page_id}/history
    return json.dumps({"results": [
        {"number": 5, "when": "2026-03-30T14:00:00Z", "by": {"displayName": "Alice"}, "message": "Updated diagrams"},
        {"number": 4, "when": "2026-03-28T09:00:00Z", "by": {"displayName": "Bob"}, "message": "Added API section"},
    ]})


@mcp.tool()
async def add_comment(confluence_token: str, page_id: str, body: str) -> str:
    """Add a comment to a Confluence page."""
    # TODO: POST /rest/api/content/{page_id}/child/comment
    return f"Comment added to page {page_id}."


if __name__ == "__main__":
    mcp.run(transport="streamable-http")
