"""Workflow Builder AI Assistant — 3-stage LLM flow via the supervisor.

Each stage rides the same supervisor→runtime pipeline chat uses, with a
dynamically-registered structured-output tool. We no longer touch
LiteLLM directly — the Claude CLI's tool-use harness now owns the
stringly-typed plumbing.

Stage 1: shortlist candidate MCP tools from bare ids.
Stage 2: narrow the shortlist using full descriptions.
Stage 3: emit a `{reply, plan}` payload. Plan ops are Pydantic-validated.

Stage 3's `plan` is a JSON-encoded string (the runtime's structured-output
schema is `str | list[str]`, no list-of-objects). We json.loads it on the
way out, then Pydantic-validates each op.
"""

from __future__ import annotations

import json
import logging

from fastapi import HTTPException, status
from pydantic import TypeAdapter, ValidationError

from app.db.models import Workflow
from app.schemas.workflow_assistant import (
    PlanOp,
    WorkflowAssistantRequest,
    WorkflowAssistantResponse,
)
from app.services import llm_runtime, mcp_catalog

logger = logging.getLogger(__name__)

_plan_adapter: TypeAdapter[list[PlanOp]] = TypeAdapter(list[PlanOp])

_HISTORY_CAP = 10
_DESCRIPTION_CAP = 400


# ---------------------------------------------------------------------------
# Entry point
# ---------------------------------------------------------------------------


async def ask(
    workflow: Workflow,
    body: WorkflowAssistantRequest,
    user_token: str,
) -> WorkflowAssistantResponse:
    model_cfg = workflow.model_config_json or {}
    backend = model_cfg.get("backend")
    model = model_cfg.get("model")
    if not backend or not model:
        raise HTTPException(
            status_code=status.HTTP_400_BAD_REQUEST,
            detail="Workflow has no default model configured",
        )
    runtime_model_config = {"backend": backend, "model": model}
    if isinstance(model_cfg.get("max_output_tokens"), int):
        runtime_model_config["max_output_tokens"] = model_cfg["max_output_tokens"]

    selected_tools = await _select_relevant_tools(
        user_message=body.user_message,
        history=body.history,
        current_definition=body.current_definition,
        model_config=runtime_model_config,
        user_token=user_token,
    )

    plan, reply = await _generate_plan(
        user_message=body.user_message,
        history=body.history,
        current_definition=body.current_definition,
        selected_tools=selected_tools,
        model_config=runtime_model_config,
        user_token=user_token,
    )

    err = _validate_plan_references(plan, body.current_definition)
    if err:
        raise HTTPException(
            status_code=status.HTTP_502_BAD_GATEWAY,
            detail=f"LLM plan failed reference validation: {err}",
        )
    _inject_workflow_defaults(plan, backend=backend, model=model)

    return WorkflowAssistantResponse(reply=reply, plan=plan)


# ---------------------------------------------------------------------------
# Stages 1 + 2: tool shortlisting
# ---------------------------------------------------------------------------


_STAGE1_SYSTEM = """\
You pick MCP tools that MIGHT be relevant to the user's workflow request.

You will see:
- The user's request (+ prior conversation)
- The current workflow state
- A list of tool identifiers in the form "{server}__{tool}" — NO descriptions

Err on the side of inclusion (up to ~20 candidates). A later stage re-verifies
with full descriptions. Only pick ids that appear in the list — do NOT invent
identifiers. If nothing applies, return an empty list.

Emit your answer via the final-response tool, with one field:
- `candidate_tool_ids` (list of strings) — the chosen ids.
"""

_STAGE2_SYSTEM = """\
You narrow a shortlist of MCP tools to the ones actually needed for the
user's workflow request.

You will see:
- The user's request (+ prior conversation)
- The current workflow state
- A shortlist of tools with their full descriptions

Pick only what's clearly useful. Only return ids from the shortlist — do NOT
invent identifiers. If nothing applies, return an empty list.

Emit your answer via the final-response tool, with one field:
- `selected_tool_ids` (list of strings) — the final subset.
"""


async def _select_relevant_tools(
    *,
    user_message: str,
    history,
    current_definition: dict,
    model_config: dict,
    user_token: str,
) -> list[dict]:
    catalog = await mcp_catalog.fetch_tools(user_token)
    if not catalog:
        return []

    stage1 = await _structured_call(
        model_config=model_config,
        user_token=user_token,
        system=(
            _STAGE1_SYSTEM
            + _format_context_block(current_definition)
            + "\n## Available tool ids\n"
            + "\n".join(f"- {t['name']}" for t in catalog)
        ),
        history=history,
        user_message=user_message,
        fields=[{
            "name": "candidate_tool_ids",
            "type": "list",
            "description": "Chosen MCP tool identifiers (empty list if none apply).",
        }],
    )
    valid = {t["name"] for t in catalog}
    candidates = [c for c in stage1.get("candidate_tool_ids", []) if c in valid]
    if not candidates:
        return []

    by_name = {t["name"]: t for t in catalog}
    detailed_block = "\n".join(
        f"- {name}: {(by_name[name].get('description') or '(no description)')[:_DESCRIPTION_CAP]}"
        for name in candidates
    )
    stage2 = await _structured_call(
        model_config=model_config,
        user_token=user_token,
        system=(
            _STAGE2_SYSTEM
            + _format_context_block(current_definition)
            + "\n## Candidate tools\n"
            + detailed_block
        ),
        history=history,
        user_message=user_message,
        fields=[{
            "name": "selected_tool_ids",
            "type": "list",
            "description": "Final subset of tools actually needed (empty list allowed).",
        }],
    )
    candidate_set = set(candidates)
    selected = [s for s in stage2.get("selected_tool_ids", []) if s in candidate_set]
    return [by_name[name] for name in selected]


# ---------------------------------------------------------------------------
# Stage 3: plan generation
# ---------------------------------------------------------------------------


_PLAN_SYSTEM = """\
You are the Aviary Workflow Builder assistant. You help a user modify a
visual workflow (a DAG of nodes and edges) by proposing a JSON plan of
edit operations.

## Node types and required `data` fields
- manual_trigger: { "label": string }
- webhook_trigger: { "label": string, "path": string }
- agent_step: {
    "label": string,
    "instruction": string,
    "mcp_tool_ids": string[],          // bind tools from the list below if needed
    "prompt_template": string,         // use "{{input}}" for upstream data
    "structured_output_fields"?: [     // OPTIONAL — see "Structured output" below
      { "name": string, "type": "str" | "list", "description"?: string }
    ]
  }
  NOTE: DO NOT emit `model_config` for agent_step. The workflow's
  default backend/model is injected automatically on the server.
- condition: { "label": string, "expression": string }
- merge: { "label": string }
- payload_parser: { "label": string, "mapping": object }
- template: { "label": string, "template": string }

## Structured output (agent_step only)
Every agent_step ALREADY emits `{ "text": "..." }` as its output — `text` is
the step's final user-facing response and is always present. Downstream nodes
reference it as `{{ input.text }}`.

Use `structured_output_fields` to (a) customize how the agent writes the
`text` field, and/or (b) ADD more named fields when downstream nodes need to
branch on or format individual pieces of the agent's answer. Rules:
- To customize the default `text` output, prepend a `{ "name": "text",
  "type": "str", "description": "..." }` entry. The description guides the
  agent on what to put in `text` (e.g. "A two-sentence summary of the
  result."). Omit the entry entirely if you don't want to override it.
- For extra fields: name each in lowercase snake_case (e.g. "severity",
  "action_items"). `type: "str"` for a single string, `type: "list"` for a
  list of strings. `description` is optional but strongly recommended.
- Keep the list short (≤4 extras). Only add a field if the plan genuinely
  uses it downstream; don't invent speculative fields.
- Reference fields in downstream templates/conditions as `{{ input.<name> }}`
  (single-upstream case) or `{{ inputs.<node_id>.<name> }}` (multi-upstream).

Example — a triage step feeding a condition:
  agent_step data: {
    "structured_output_fields": [
      { "name": "text", "type": "str",
        "description": "One-line summary of the triage decision." },
      { "name": "severity", "type": "str",
        "description": "One of: low, medium, high." }
    ]
  }
  downstream condition expression: `input.severity == "high"`

## Operation vocabulary (items of the `plan` array)
{ "op": "add_node", "id": "<new_unique_id>", "type": "<node_type>",
  "position": { "x": <number>, "y": <number> }, "data": { ... } }
{ "op": "update_node", "id": "<existing_id>", "data_patch": { ... } }
{ "op": "delete_node", "id": "<existing_id>" }
{ "op": "add_edge", "source": "<node_id>", "target": "<node_id>" }
{ "op": "delete_edge", "id": "<existing_edge_id>" }

## Rules
1. Emit operations in dependency order. add_node must come BEFORE any
   add_edge that references it.
2. New node ids must be unique across both the current state AND the plan.
   Use descriptive snake_case ids (e.g. "summarize_step").
3. add_edge source/target must resolve to ids that exist after the
   preceding operations (existing nodes or newly added ones, not deleted).
4. DO NOT re-emit nodes the user did not ask to change — emit only deltas.
5. Place new nodes at readable positions (~200px spacing from related
   existing nodes, growing left-to-right or top-to-bottom).
6. For a pure question, return plan_json="[]" and put the answer in `reply`.
7. For an ambiguous request, ask a clarifying question and return plan_json="[]".
8. Every workflow needs exactly one trigger node unless the user is
   building pieces incrementally.
9. On agent_step, only bind tools from the "Available MCP tools" list
   below. If no tools are listed, leave `mcp_tool_ids: []`.

## Output contract
Emit your answer via the final-response tool, with exactly two fields:
- `reply` (string): a short natural-language message to the user.
- `plan_json` (string): a JSON-encoded array of the edit operations
  above. MUST be valid JSON and parse to an array (possibly empty). Do
  NOT wrap it in markdown code fences.
"""


async def _generate_plan(
    *,
    user_message: str,
    history,
    current_definition: dict,
    selected_tools: list[dict],
    model_config: dict,
    user_token: str,
) -> tuple[list[PlanOp], str]:
    parsed = await _structured_call(
        model_config=model_config,
        user_token=user_token,
        system=(
            _PLAN_SYSTEM
            + _format_context_block(current_definition)
            + _format_tools_block(selected_tools)
        ),
        history=history,
        user_message=user_message,
        fields=[
            {
                "name": "reply",
                "type": "str",
                "description": "Short user-facing message describing the change.",
            },
            {
                "name": "plan_json",
                "type": "str",
                "description": 'JSON-encoded array of edit operations. Use "[]" for no-op.',
            },
        ],
    )

    reply = parsed.get("reply", "")
    if not isinstance(reply, str):
        reply = str(reply)

    plan_json = parsed.get("plan_json", "[]")
    if not isinstance(plan_json, str):
        raise HTTPException(
            status_code=status.HTTP_502_BAD_GATEWAY,
            detail=f"LLM plan_json must be a string, got {type(plan_json).__name__}",
        )
    try:
        plan_raw = json.loads(plan_json)
    except json.JSONDecodeError as e:
        raise HTTPException(
            status_code=status.HTTP_502_BAD_GATEWAY,
            detail=f"LLM plan_json is not valid JSON: {e}",
        ) from e
    if not isinstance(plan_raw, list):
        raise HTTPException(
            status_code=status.HTTP_502_BAD_GATEWAY,
            detail="LLM plan_json must decode to a list",
        )
    try:
        plan = _plan_adapter.validate_python(plan_raw)
    except ValidationError as e:
        raise HTTPException(
            status_code=status.HTTP_502_BAD_GATEWAY,
            detail=f"LLM plan failed schema validation: {e.errors()}",
        ) from e

    return plan, reply


# ---------------------------------------------------------------------------
# Supervisor-backed call
# ---------------------------------------------------------------------------


async def _structured_call(
    *,
    model_config: dict,
    user_token: str,
    system: str,
    history,
    user_message: str,
    fields: list[dict],
) -> dict:
    system_with_history = system + _format_history_block(history)
    try:
        return await llm_runtime.call_structured(
            model_config=model_config,
            system=system_with_history,
            user_message=user_message,
            fields=fields,
            user_token=user_token,
        )
    except llm_runtime.LLMRuntimeError as e:
        logger.warning("Workflow assistant LLM call failed: %s", e)
        raise HTTPException(
            status_code=status.HTTP_502_BAD_GATEWAY,
            detail=f"LLM gateway error: {e}",
        ) from e


# ---------------------------------------------------------------------------
# Formatting helpers
# ---------------------------------------------------------------------------


def _format_context_block(current_definition: dict) -> str:
    state = json.dumps(
        {
            "nodes": current_definition.get("nodes", []),
            "edges": current_definition.get("edges", []),
        },
        ensure_ascii=False,
    )
    return f"\n## Current workflow state\n{state}"


def _format_tools_block(selected_tools: list[dict]) -> str:
    if not selected_tools:
        return "\n## Available MCP tools\n(none — do not bind any tool ids)"
    lines = ["\n## Available MCP tools"]
    for t in selected_tools:
        desc = (t.get("description") or "(no description)")[:_DESCRIPTION_CAP]
        lines.append(f"- {t['name']}: {desc}")
    return "\n".join(lines)


def _format_history_block(history) -> str:
    """Fold prior assistant turns into a single system-prompt context block.

    The runtime path is one-shot per call (a fresh Claude session each
    time), so we can't rely on CLI-side conversation state. Prior turns
    live in the system prompt as context; the current user message is
    sent as the single user turn.
    """
    turns = list(history or [])[-_HISTORY_CAP:]
    if not turns:
        return ""
    lines = ["\n## Prior conversation (for context only)"]
    for turn in turns:
        role = turn.role.capitalize()
        lines.append(f"### {role}\n{turn.content}")
    return "\n".join(lines)


# ---------------------------------------------------------------------------
# Post-processing
# ---------------------------------------------------------------------------


def _inject_workflow_defaults(plan: list[PlanOp], backend: str, model: str) -> None:
    default_cfg = {"backend": backend, "model": model}
    for op in plan:
        if op.op == "add_node" and op.type == "agent_step":
            op.data["model_config"] = dict(default_cfg)


def _validate_plan_references(plan: list[PlanOp], definition: dict) -> str | None:
    nodes_raw = definition.get("nodes", [])
    edges_raw = definition.get("edges", [])

    live_nodes: set[str] = {
        n["id"] for n in nodes_raw if isinstance(n, dict) and isinstance(n.get("id"), str)
    }
    live_edges: set[str] = {
        e["id"] for e in edges_raw if isinstance(e, dict) and isinstance(e.get("id"), str)
    }

    for i, op in enumerate(plan):
        if op.op == "add_node":
            if op.id in live_nodes:
                return f"step[{i}] add_node id '{op.id}' conflicts with existing node"
            live_nodes.add(op.id)
        elif op.op == "update_node":
            if op.id not in live_nodes:
                return f"step[{i}] update_node id '{op.id}' does not exist"
        elif op.op == "delete_node":
            if op.id not in live_nodes:
                return f"step[{i}] delete_node id '{op.id}' does not exist"
            live_nodes.discard(op.id)
        elif op.op == "add_edge":
            if op.source not in live_nodes:
                return f"step[{i}] add_edge source '{op.source}' does not exist"
            if op.target not in live_nodes:
                return f"step[{i}] add_edge target '{op.target}' does not exist"
            if op.id:
                if op.id in live_edges:
                    return f"step[{i}] add_edge id '{op.id}' conflicts"
                live_edges.add(op.id)
        elif op.op == "delete_edge":
            if op.id not in live_edges:
                return f"step[{i}] delete_edge id '{op.id}' does not exist"
            live_edges.discard(op.id)
    return None
