"""GitHub MCP Server.

Standard FastMCP server. Each tool is a plain async function with typed
arguments and a docstring — no platform boilerplate required.

The `github_token` argument on every tool is injected by the MCP Gateway
from Vault at call time. Tool authors just declare it as a parameter.
"""

import json
from mcp.server.fastmcp import FastMCP

mcp = FastMCP("github", host="0.0.0.0", port=8000, stateless_http=True)


# ── Credential provisioning ─────────────────────────────────


@mcp.tool()
async def setup_git_credentials(github_token: str, repo: str | None = None) -> str:
    """Configure git credentials in the agent workspace for GitHub access.
    After calling this, the agent can use git clone/pull/push directly via shell.
    """
    # TODO: support fine-grained tokens scoped to `repo`
    return (
        "Git credentials configured. Run the following commands:\n\n"
        "```bash\n"
        "git config --global credential.helper store\n"
        f"echo 'https://x-access-token:{github_token}@github.com' > ~/.git-credentials\n"
        "```\n\n"
        "After running these, git clone/pull/push to GitHub will authenticate automatically."
    )


# ── Pull Requests ───────────────────────────────────────────


@mcp.tool()
async def create_pull_request(
    github_token: str, repo: str, title: str, head: str,
    body: str = "", base: str = "main", draft: bool = False,
) -> str:
    """Create a pull request on GitHub."""
    # TODO: POST https://api.github.com/repos/{repo}/pulls
    return json.dumps({"number": 42, "html_url": f"https://github.com/{repo}/pull/42", "state": "open"})


@mcp.tool()
async def list_pull_requests(github_token: str, repo: str, state: str = "open") -> str:
    """List pull requests for a repository."""
    # TODO: GET https://api.github.com/repos/{repo}/pulls?state={state}
    return json.dumps([
        {"number": 42, "title": "Add auth module", "state": "open", "user": {"login": "dev1"}},
        {"number": 41, "title": "Fix login bug", "state": "open", "user": {"login": "dev2"}},
    ])


@mcp.tool()
async def get_pull_request(github_token: str, repo: str, number: int) -> str:
    """Get details of a specific pull request including diff stats."""
    # TODO: GET https://api.github.com/repos/{repo}/pulls/{number}
    return json.dumps({"number": number, "title": "Add auth module", "state": "open",
                        "additions": 150, "deletions": 30, "changed_files": 8, "mergeable": True})


@mcp.tool()
async def merge_pull_request(github_token: str, repo: str, number: int, merge_method: str = "squash") -> str:
    """Merge a pull request."""
    # TODO: PUT https://api.github.com/repos/{repo}/pulls/{number}/merge
    return json.dumps({"merged": True, "sha": "abc123def456", "message": "Squash and merge"})


@mcp.tool()
async def list_pr_reviews(github_token: str, repo: str, number: int) -> str:
    """List reviews on a pull request."""
    # TODO: GET https://api.github.com/repos/{repo}/pulls/{number}/reviews
    return json.dumps([{"user": {"login": "reviewer1"}, "state": "APPROVED"}])


@mcp.tool()
async def create_pr_review(github_token: str, repo: str, number: int, event: str, body: str = "") -> str:
    """Submit a review on a pull request (approve, request changes, or comment)."""
    # TODO: POST https://api.github.com/repos/{repo}/pulls/{number}/reviews
    return json.dumps({"id": 1001, "state": event})


# ── Issues ──────────────────────────────────────────────────


@mcp.tool()
async def create_issue(
    github_token: str, repo: str, title: str,
    body: str = "", labels: list[str] | None = None, assignees: list[str] | None = None,
) -> str:
    """Create a new issue on GitHub."""
    # TODO: POST https://api.github.com/repos/{repo}/issues
    return json.dumps({"number": 101, "html_url": f"https://github.com/{repo}/issues/101"})


@mcp.tool()
async def list_issues(
    github_token: str, repo: str, state: str = "open",
    labels: str | None = None, assignee: str | None = None,
) -> str:
    """List issues for a repository."""
    # TODO: GET https://api.github.com/repos/{repo}/issues?state={state}
    return json.dumps([
        {"number": 101, "title": "Auth timeout on mobile", "state": "open", "labels": [{"name": "bug"}]},
        {"number": 100, "title": "Add dark mode support", "state": "open", "labels": [{"name": "enhancement"}]},
    ])


@mcp.tool()
async def get_issue(github_token: str, repo: str, number: int) -> str:
    """Get details of a specific issue."""
    # TODO: GET https://api.github.com/repos/{repo}/issues/{number}
    return json.dumps({"number": number, "title": "Auth timeout on mobile", "state": "open",
                        "body": "Users report auth timeouts...", "labels": [{"name": "bug"}]})


@mcp.tool()
async def add_issue_comment(github_token: str, repo: str, number: int, body: str) -> str:
    """Add a comment to an issue or pull request."""
    # TODO: POST https://api.github.com/repos/{repo}/issues/{number}/comments
    return json.dumps({"id": 5001, "created_at": "2026-04-05T12:00:00Z"})


# ── Code & Repository ───────────────────────────────────────


@mcp.tool()
async def get_file_contents(github_token: str, repo: str, path: str, ref: str = "main") -> str:
    """Get contents of a file from a GitHub repository via API (without cloning)."""
    # TODO: GET https://api.github.com/repos/{repo}/contents/{path}?ref={ref}
    return "# README\n\nThis is the project README file.\n\n## Getting Started\n\n```bash\nnpm install\n```"


@mcp.tool()
async def search_code(github_token: str, query: str, repo: str | None = None) -> str:
    """Search for code across GitHub repositories."""
    # TODO: GET https://api.github.com/search/code?q={query}
    return json.dumps({"total_count": 3, "items": [
        {"repository": {"full_name": "org/repo"}, "path": "src/auth.py"},
    ]})


@mcp.tool()
async def list_repos(github_token: str, owner: str, type: str = "all", sort: str = "updated") -> str:
    """List repositories for a user or organization."""
    # TODO: GET https://api.github.com/users/{owner}/repos
    return json.dumps([
        {"full_name": f"{owner}/frontend", "description": "Web application", "language": "TypeScript"},
        {"full_name": f"{owner}/backend", "description": "API server", "language": "Python"},
    ])


@mcp.tool()
async def get_repo(github_token: str, repo: str) -> str:
    """Get repository metadata (description, stars, language, default branch)."""
    # TODO: GET https://api.github.com/repos/{repo}
    return json.dumps({"full_name": repo, "description": "Main project", "language": "Python",
                        "default_branch": "main", "stargazers_count": 42})


if __name__ == "__main__":
    mcp.run(transport="streamable-http")
