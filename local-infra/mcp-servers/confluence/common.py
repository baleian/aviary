"""Shared HTTP plumbing + markdown→storage converter for the Confluence MCP
server (cloud + legacy variants).

The Confluence "storage" XHTML format is the same on Cloud and Server / DC,
so the conversion lives here. Endpoint paths and request shapes differ —
those live in `cloud.py` / `legacy.py`.
"""

import base64
import html
import json
import logging
import os
import re
from typing import Any

import httpx
from markdown_it import MarkdownIt

# Quiet per-request access logs + FastMCP internal chatter; both fire many
# times per second under normal use and drown the dev console.
logging.getLogger("uvicorn.access").disabled = True
logging.getLogger("mcp").setLevel(logging.WARNING)

CONFLUENCE_BASE_URL = os.environ["CONFLUENCE_BASE_URL"].rstrip("/")


_http_client: httpx.AsyncClient | None = None


def client() -> httpx.AsyncClient:
    global _http_client
    if _http_client is None:
        _http_client = httpx.AsyncClient(base_url=CONFLUENCE_BASE_URL, timeout=30.0)
    return _http_client


def basic_auth(token: str) -> str:
    raw = token.encode("utf-8")
    return "Basic " + base64.b64encode(raw).decode("ascii")


async def request(
    method: str,
    path: str,
    *,
    token: str,
    json_body: Any = None,
    params: dict | None = None,
) -> dict | list | str:
    """Issue an authenticated Confluence API request.

    Returns parsed JSON on 2xx, or an "ERROR: ..." string on failure. Tools
    propagate that string as their return value.
    """
    headers = {
        "Authorization": basic_auth(token),
        "Accept": "application/json",
    }
    if json_body is not None:
        headers["Content-Type"] = "application/json"
    try:
        resp = await client().request(
            method, path, headers=headers, json=json_body, params=params
        )
    except httpx.HTTPError as e:
        return f"ERROR: HTTP request failed: {e}"
    if resp.status_code >= 400:
        body = resp.text[:500] if resp.text else ""
        return f"ERROR: {resp.status_code} {resp.reason_phrase} — {body}"
    if not resp.content:
        return {}
    try:
        return resp.json()
    except ValueError:
        return {"text": resp.text}


def result(value: dict | list | str) -> str:
    if isinstance(value, str):
        return value
    return json.dumps(value)


# ── Markdown → Confluence storage format ───────────────────────

_md = MarkdownIt("commonmark").enable("table").enable("strikethrough")

_FENCE_RE = re.compile(
    r'<pre><code(?: class="language-([^"]+)")?>(.*?)</code></pre>',
    re.DOTALL,
)


def _replace_fence(m: re.Match) -> str:
    lang = m.group(1) or ""
    code = html.unescape(m.group(2) or "")
    code = code.rstrip("\n")
    # CDATA can't contain "]]>"
    code = code.replace("]]>", "]]]]><![CDATA[>")
    parts = ['<ac:structured-macro ac:name="code">']
    if lang:
        parts.append(f'<ac:parameter ac:name="language">{lang}</ac:parameter>')
    parts.append(f'<ac:plain-text-body><![CDATA[{code}]]></ac:plain-text-body>')
    parts.append("</ac:structured-macro>")
    return "".join(parts)


def md_to_storage(md: str) -> str:
    """Convert markdown to Confluence storage format (XHTML).

    Standard markdown → HTML via markdown-it-py (CommonMark + GFM tables +
    strikethrough), then post-processed to wrap fenced code blocks in
    Confluence's native <ac:structured-macro ac:name="code"> macro.

    If the input already starts with '<' (after stripping whitespace) it is
    treated as raw storage XML and passed through unchanged — letting power
    users hand-write storage when they need macros the converter doesn't know
    about.
    """
    if not md:
        return ""
    if md.lstrip().startswith("<"):
        return md
    html_out = _md.render(md)
    return _FENCE_RE.sub(_replace_fence, html_out)
