"""Confluence MCP server entry point.

CONFLUENCE_API_VARIANT picks the API surface:
  - cloud (default): Atlassian Cloud, /wiki/api/v2 (+ a couple of v1 fallbacks).
  - legacy        : Server / Data Center, /rest/api/{content,space,search}.

CONFLUENCE_BASE_URL must point at the matching site.
"""

import os

variant = os.environ.get("CONFLUENCE_API_VARIANT", "cloud").lower()

if variant == "cloud":
    from cloud import mcp
elif variant == "legacy":
    from legacy import mcp
else:
    raise RuntimeError(
        f"CONFLUENCE_API_VARIANT must be 'cloud' or 'legacy', got {variant!r}"
    )


if __name__ == "__main__":
    mcp.run(transport="streamable-http")
