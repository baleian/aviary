"""Jira MCP Server.

Standard FastMCP server for Jira project management operations.
The `jira_token` argument on every tool is injected by the MCP Gateway.
"""

import json
from mcp.server.fastmcp import FastMCP

mcp = FastMCP("jira", host="0.0.0.0", port=8000, stateless_http=True)


@mcp.tool()
async def get_issue(jira_token: str, issue_key: str, fields: list[str] | None = None) -> str:
    """Get details of a Jira issue."""
    # TODO: GET /rest/api/2/issue/{issue_key}
    return json.dumps({
        "key": issue_key,
        "fields": {"summary": "Implement user authentication", "status": {"name": "In Progress"},
                    "priority": {"name": "High"}, "assignee": {"displayName": "John Doe"}},
    })


@mcp.tool()
async def create_issue(
    jira_token: str, project: str, summary: str, description: str = "",
    issue_type: str = "Task", priority: str = "Medium",
    assignee: str | None = None, labels: list[str] | None = None,
) -> str:
    """Create a new Jira issue."""
    # TODO: POST /rest/api/2/issue
    return json.dumps({"key": f"{project}-456", "self": f"https://jira.example.com/rest/api/2/issue/{project}-456"})


@mcp.tool()
async def update_issue(
    jira_token: str, issue_key: str,
    summary: str | None = None, description: str | None = None,
    priority: str | None = None, assignee: str | None = None,
    labels: list[str] | None = None,
) -> str:
    """Update fields on an existing Jira issue."""
    # TODO: PUT /rest/api/2/issue/{issue_key}
    return f"Issue {issue_key} updated successfully."


@mcp.tool()
async def transition_issue(jira_token: str, issue_key: str, transition: str, comment: str = "") -> str:
    """Move an issue to a different status (e.g., In Progress, Done)."""
    # TODO: POST /rest/api/2/issue/{issue_key}/transitions
    return f"Issue {issue_key} transitioned to '{transition}'."


@mcp.tool()
async def add_comment(jira_token: str, issue_key: str, body: str) -> str:
    """Add a comment to a Jira issue."""
    # TODO: POST /rest/api/2/issue/{issue_key}/comment
    return f"Comment added to {issue_key}."


@mcp.tool()
async def search_issues(
    jira_token: str, jql: str, max_results: int = 20, fields: list[str] | None = None,
) -> str:
    """Search for issues using JQL (Jira Query Language)."""
    # TODO: POST /rest/api/2/search
    return json.dumps({"total": 2, "issues": [
        {"key": "PROJ-123", "fields": {"summary": "Implement auth", "status": {"name": "In Progress"}}},
        {"key": "PROJ-124", "fields": {"summary": "Add logging", "status": {"name": "To Do"}}},
    ]})


@mcp.tool()
async def assign_issue(jira_token: str, issue_key: str, assignee: str) -> str:
    """Assign an issue to a user."""
    # TODO: PUT /rest/api/2/issue/{issue_key}/assignee
    return f"Issue {issue_key} assigned to {assignee}."


@mcp.tool()
async def link_issues(jira_token: str, inward_issue: str, outward_issue: str, link_type: str) -> str:
    """Create a link between two issues."""
    # TODO: POST /rest/api/2/issueLink
    return f"Link created: {inward_issue} {link_type} {outward_issue}."


@mcp.tool()
async def list_sprints(jira_token: str, board_id: int, state: str = "active") -> str:
    """List sprints for a board."""
    # TODO: GET /rest/agile/1.0/board/{board_id}/sprint?state={state}
    return json.dumps([{"id": 10, "name": "Sprint 5", "state": "active",
                         "startDate": "2026-03-25", "endDate": "2026-04-08"}])


@mcp.tool()
async def get_sprint_issues(jira_token: str, sprint_id: int, status: str | None = None) -> str:
    """Get all issues in a sprint."""
    # TODO: GET /rest/agile/1.0/sprint/{sprint_id}/issue
    return json.dumps({"total": 5, "issues": [
        {"key": "PROJ-120", "summary": "Setup CI/CD", "status": "Done"},
        {"key": "PROJ-121", "summary": "Database migration", "status": "In Progress"},
    ]})


if __name__ == "__main__":
    mcp.run(transport="streamable-http")
