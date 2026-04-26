"""One-shot LLM helpers that ride the supervisor→runtime pipeline so
internal features (workflow assistant, agent auto-complete, …) inherit
the Claude CLI's tool-use harness. The runtime registers each
`structured_outputs[]` entry as an in-process MCP tool; the CLI calls
them as regular tool_use events which land in `assembled_blocks` as
`tool_call` blocks.

Each call is ephemeral: fresh (session_id, agent_id) UUIDs, no tools,
no MCP servers (besides the dynamic structured-output ones), no
Claude-session resume.
"""

from __future__ import annotations

import asyncio
import logging
import uuid
from typing import Iterable

import httpx

from app.services import agent_supervisor

logger = logging.getLogger(__name__)


class LLMRuntimeError(RuntimeError):
    """Raised when the supervisor/runtime path fails."""


_CALL_TIMEOUT_S = 300.0
_SYSTEM_MCP_SERVER = "system"


def structured_tool_cli_name(tool_name: str) -> str:
    return f"mcp__{_SYSTEM_MCP_SERVER}__{tool_name}"


async def run_once(
    *,
    model_config: dict,
    system: str,
    user_message: str,
    structured_outputs: list[dict] | None = None,
    history_turns: Iterable[dict] | None = None,
    user_token: str,
    session_id: str | None = None,
) -> dict:
    """Invoke the runtime once and return the raw supervisor response
    (including `assembled_text`, `assembled_blocks`, etc.).

    `structured_outputs` is forwarded verbatim to the runtime so the
    caller fully controls tool naming, descriptions, and fields.
    `history_turns` — optional prior [{role, content}, …] — is folded
    into the system prompt as context since each runtime call is a
    fresh Claude session. Pass `session_id` to let the caller
    pre-subscribe to the supervisor's Redis event channel (used by
    streaming endpoints); otherwise one is generated.
    """
    if session_id is None:
        session_id = str(uuid.uuid4())
    agent_id = f"aviary-helper:{session_id}"

    system_with_history = system + _format_history(history_turns)

    body: dict = {
        "session_id": session_id,
        "content_parts": [{"text": user_message}],
        "agent_config": {
            "agent_id": agent_id,
            "runtime_endpoint": None,
            "model_config": model_config,
            "instruction": system_with_history,
            "tools": [],
            "mcp_servers": {},
        },
    }
    if structured_outputs:
        body["structured_outputs"] = structured_outputs

    try:
        result = await agent_supervisor.post_message(
            session_id=session_id, body=body,
            user_token=user_token, timeout=_CALL_TIMEOUT_S,
        )
    except httpx.HTTPError as e:
        raise LLMRuntimeError(f"Supervisor request failed: {e}") from e

    # Fire-and-forget workspace cleanup — ephemeral (session, agent) dirs
    # on the shared PVC would otherwise accumulate forever.
    asyncio.create_task(_cleanup_quiet(session_id, agent_id))

    if result.get("status") == "error":
        raise LLMRuntimeError(result.get("message") or "Runtime error")
    return result


def find_tool_call(result: dict, tool_name: str) -> dict | None:
    """Return the first `tool_call` block whose name matches `tool_name`
    (already CLI-prefixed), or None if the model didn't invoke it.

    Inputs live on `block["input"]`. Callers that validate further can
    do so on the returned dict.
    """
    for block in result.get("assembled_blocks") or []:
        if block.get("type") != "tool_call":
            continue
        if block.get("name") == tool_name:
            return block
    return None


def find_structured_tool_call(result: dict, tool_name: str) -> dict | None:
    return find_tool_call(result, structured_tool_cli_name(tool_name))


def _format_history(turns: Iterable[dict] | None) -> str:
    if not turns:
        return ""
    lines = ["\n\n## Prior conversation (for context only)"]
    for turn in turns:
        role = str(turn.get("role", "user")).capitalize()
        content = str(turn.get("content", ""))
        lines.append(f"### {role}\n{content}")
    return "\n".join(lines)


async def _cleanup_quiet(session_id: str, agent_id: str) -> None:
    try:
        await agent_supervisor.cleanup_session(session_id, agent_id)
    except Exception:  # noqa: BLE001
        logger.debug("helper cleanup failed for %s/%s", session_id, agent_id, exc_info=True)
