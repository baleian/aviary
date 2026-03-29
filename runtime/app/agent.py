"""Agent runner using the official claude-agent-sdk package.

All inference is routed through the Inference Router (platform namespace):
  claude-agent-sdk → Claude Code CLI → Anthropic SDK
    → POST http://inference-router.platform.svc:8080/v1/messages
    → Router inspects model name → proxies to correct backend

Multi-turn conversation is maintained via the SDK's session management:
  - First message: new session, session_id stored to /workspace/.session_id
  - Subsequent messages: continue_conversation=True resumes the session
  - Pod restart with same PVC: session_id recovered from file
"""

import json
import logging
import os
from pathlib import Path
from typing import AsyncGenerator

logger = logging.getLogger(__name__)

from claude_agent_sdk import (
    AssistantMessage,
    ClaudeAgentOptions,
    ResultMessage,
    TextBlock,
    ToolResultBlock,
    ToolUseBlock,
    query,
)

from app.history import append_message

# Agent config paths (mounted from ConfigMap)
CONFIG_DIR = Path("/agent/config")
WORKSPACE_DIR = Path("/workspace")
SESSION_ID_FILE = WORKSPACE_DIR / ".session_id"

# Inference Router URL (K8s Service in platform namespace)
INFERENCE_ROUTER_URL = os.environ.get(
    "INFERENCE_ROUTER_URL",
    "http://inference-router.platform.svc:8080",
)


def load_agent_config() -> dict:
    """Load agent configuration from mounted ConfigMap."""
    config = {}

    instruction_file = CONFIG_DIR / "instruction.md"
    if instruction_file.exists():
        config["instruction"] = instruction_file.read_text()

    tools_file = CONFIG_DIR / "tools.json"
    if tools_file.exists():
        config["tools"] = json.loads(tools_file.read_text())

    policy_file = CONFIG_DIR / "policy.json"
    if policy_file.exists():
        config["policy"] = json.loads(policy_file.read_text())

    mcp_file = CONFIG_DIR / "mcp-servers.json"
    if mcp_file.exists():
        config["mcp_servers"] = json.loads(mcp_file.read_text())

    return config


def _load_session_id() -> str | None:
    """Load the SDK session ID from the persistent workspace."""
    if SESSION_ID_FILE.exists():
        sid = SESSION_ID_FILE.read_text().strip()
        return sid if sid else None
    return None


def _save_session_id(session_id: str) -> None:
    """Save the SDK session ID to the persistent workspace."""
    SESSION_ID_FILE.write_text(session_id)


def _clear_session_id() -> None:
    """Remove a stale SDK session ID (e.g. after cluster restart)."""
    if SESSION_ID_FILE.exists():
        SESSION_ID_FILE.unlink()
        logger.info("Cleared stale session ID file")


def _build_options(agent_config: dict, model_config: dict) -> ClaudeAgentOptions:
    """Build ClaudeAgentOptions routing all inference through the Inference Router."""
    model = model_config.get("model", "claude-sonnet-4-20250514")

    # Check if we have an existing session to continue
    existing_session_id = _load_session_id()

    opts = ClaudeAgentOptions(
        model=model,
        system_prompt=agent_config.get("instruction"),
        cwd=WORKSPACE_DIR,
        permission_mode="bypassPermissions",
        include_partial_messages=False,
        # Multi-turn: resume existing session or start new one
        # resume=<session_id> continues the specific session (not continue_conversation)
        resume=existing_session_id,
        # Route all Anthropic API calls through the Inference Router
        env={
            "ANTHROPIC_BASE_URL": INFERENCE_ROUTER_URL,
            "ANTHROPIC_API_KEY": "routed-via-inference-router",
        },
    )

    # Tools
    tools_list = agent_config.get("tools")
    if tools_list:
        opts.allowed_tools = tools_list

    return opts


async def process_message(
    content: str,
    model_config: dict | None = None,
    agent_config_from_api: dict | None = None,
) -> AsyncGenerator[dict, None]:
    """Process a user message through claude-agent-sdk.

    Agent config is received from the API server (sourced from DB) on every
    message, ensuring edits to instruction/tools take effect immediately
    without Pod restart. Falls back to ConfigMap if not provided.

    Yields SSE-formatted dicts:
      {"type": "chunk", "content": "..."}
      {"type": "tool_use", "name": "...", "input": {...}}
      {"type": "tool_result", "tool_use_id": "...", "content": "..."}
    """
    agent_config = agent_config_from_api if agent_config_from_api else load_agent_config()
    append_message("user", content)

    mc = model_config or {"backend": "claude", "model": "claude-sonnet-4-20250514"}
    options = _build_options(agent_config, mc)

    full_response = ""

    async def _run_query(opts: ClaudeAgentOptions) -> AsyncGenerator[dict, None]:
        nonlocal full_response
        async for message in query(prompt=content, options=opts):
            if isinstance(message, AssistantMessage):
                for block in message.content:
                    if isinstance(block, TextBlock):
                        full_response += block.text
                        yield {"type": "chunk", "content": block.text}
                    elif isinstance(block, ToolUseBlock):
                        yield {
                            "type": "tool_use",
                            "name": block.name,
                            "input": block.input,
                        }
                    elif isinstance(block, ToolResultBlock):
                        result_text = block.content if isinstance(block.content, str) else json.dumps(block.content)
                        yield {
                            "type": "tool_result",
                            "tool_use_id": block.tool_use_id,
                            "content": result_text,
                        }

            elif isinstance(message, ResultMessage):
                # Save session_id for multi-turn continuation
                if message.session_id:
                    _save_session_id(message.session_id)

                if message.result and not full_response:
                    full_response = message.result
                    yield {"type": "chunk", "content": message.result}

    try:
        async for chunk in _run_query(options):
            yield chunk
    except Exception as e:
        # If resume failed (stale session after pod/cluster restart),
        # clear the stale session ID and retry as a fresh session
        if options.resume is not None:
            logger.warning(
                "Session resume failed (session_id=%s), retrying as new session: %s",
                options.resume, e,
            )
            _clear_session_id()
            options.resume = None
            full_response = ""
            try:
                async for chunk in _run_query(options):
                    yield chunk
            except Exception as retry_err:
                backend = mc.get("backend", "claude")
                model = mc.get("model", "unknown")
                error_msg = f"[{backend}/{model}] Error: {retry_err}"
                yield {"type": "chunk", "content": error_msg}
                append_message("assistant", error_msg)
                return
        else:
            backend = mc.get("backend", "claude")
            model = mc.get("model", "unknown")
            error_msg = f"[{backend}/{model}] Error: {e}"
            yield {"type": "chunk", "content": error_msg}
            append_message("assistant", error_msg)
            return

    append_message("assistant", full_response)
