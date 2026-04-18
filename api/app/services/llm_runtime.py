"""One-shot LLM helper that rides the same supervisor→runtime pipeline
chat uses, so internal LLM features (workflow assistant, agent
auto-complete, …) inherit the Claude CLI's tool-use harness and the
runtime's structured-output tool for free.

Each call is ephemeral: fresh (session_id, agent_id) UUIDs, no tools,
no MCP servers, no Claude-session resume. The only thing the runtime
registers is the dynamic structured-output MCP tool built from `fields`.
"""

from __future__ import annotations

import asyncio
import logging
import uuid

import httpx

from app.services import agent_supervisor

logger = logging.getLogger(__name__)


class LLMRuntimeError(RuntimeError):
    """Raised when the supervisor/runtime path fails to return structured output."""


# Same budget the previous direct-LiteLLM path used — Claude CLI cold
# starts can eat a few seconds before tokens flow.
_CALL_TIMEOUT_S = 300.0


async def call_structured(
    *,
    model_config: dict,
    system: str,
    user_message: str,
    fields: list[dict],
    user_token: str,
) -> dict:
    """Invoke the runtime with a structured-output tool and return its payload.

    `model_config` mirrors the shape chat/workflow nodes send
    (`{backend, model, max_output_tokens?}`). `fields` is the
    `structured_output_format.fields` list — each entry
    `{name, type: "str"|"list", description?}`. The return value is the
    dict the model emitted via the final-response tool.
    """
    if not fields:
        raise ValueError("llm_runtime.call_structured requires at least one field")

    session_id = str(uuid.uuid4())
    agent_id = f"aviary-helper:{session_id}"

    body = {
        "session_id": session_id,
        "content_parts": [{"text": user_message}],
        "agent_config": {
            "agent_id": agent_id,
            "runtime_endpoint": None,
            "model_config": model_config,
            "instruction": system,
            "tools": [],
            "mcp_servers": {},
        },
        "structured_output_format": {"fields": fields},
    }

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

    status = result.get("status")
    if status == "error":
        raise LLMRuntimeError(result.get("message") or "Runtime error")

    payload = result.get("structured_output")
    if not isinstance(payload, dict) or not payload:
        raise LLMRuntimeError(
            f"Runtime returned no structured output (status={status!r}); "
            "model likely skipped the final-response tool call"
        )
    return payload


async def _cleanup_quiet(session_id: str, agent_id: str) -> None:
    try:
        await agent_supervisor.cleanup_session(session_id, agent_id)
    except Exception:  # noqa: BLE001
        logger.debug("helper cleanup failed for %s/%s", session_id, agent_id, exc_info=True)
