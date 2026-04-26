"""Jira MCP server entry point.

JIRA_API_VARIANT picks the API surface:
  - cloud (default): Atlassian Cloud, REST API v3 + ADF.
  - legacy        : Server / Data Center, REST API v2 + plain text.

JIRA_BASE_URL must point at the matching site.
"""

import os

variant = os.environ.get("JIRA_API_VARIANT", "cloud").lower()

if variant == "cloud":
    from cloud import mcp
elif variant == "legacy":
    from legacy import mcp
else:
    raise RuntimeError(
        f"JIRA_API_VARIANT must be 'cloud' or 'legacy', got {variant!r}"
    )


if __name__ == "__main__":
    mcp.run(transport="streamable-http")
