/**
 * Mock agent — scenario-driven SSE stream generator for integration tests.
 *
 * Activated when RUNTIME_MODE=test. File I/O steps (write/read/ls/exec) run
 * through the SAME bwrap sandbox as the real agent, so isolation, resume, and
 * A2A file sharing can be verified end-to-end without invoking an LLM.
 *
 * Scenario is passed in every message body as `agent_config.mock_scenario`.
 */

import * as fs from "node:fs";
import { spawnSync } from "node:child_process";
import {
  sessionClaudeDir,
  sessionTmp,
  sessionWorkspace,
} from "./constants.js";
import type { SSEChunk } from "./agent.js";

// Single source of truth for the sandbox mount namespace (see claude-sandbox.sh).
const SANDBOX_SCRIPT = "/app/scripts/sandbox.sh";

type MockStep =
  | { type: "chunk"; content: string; delay_ms?: number }
  | { type: "thinking"; content: string }
  | { type: "tool_use"; name: string; input?: unknown }
  | { type: "write"; path: string; content: string }
  | { type: "read"; path: string }
  | { type: "ls"; path?: string }
  | { type: "exec"; cmd: string[] }
  | { type: "sleep"; ms: number }
  | { type: "error"; message: string };

interface MockScenario {
  steps: MockStep[];
}

interface AgentConfig {
  mock_scenario?: MockScenario;
}

interface ContentPart {
  text?: string;
  attachments?: unknown[];
}

export interface ExecResult {
  stdout: string;
  stderr: string;
  code: number;
}

/** Run a command inside the same bwrap namespace the real agent uses. */
export function runInSandbox(sessionId: string, cmd: string[]): ExecResult {
  const ws = sessionWorkspace(sessionId);
  const cd = sessionClaudeDir(sessionId);
  const tm = sessionTmp(sessionId);
  fs.mkdirSync(ws, { recursive: true });
  fs.mkdirSync(cd, { recursive: true });
  fs.mkdirSync(tm, { recursive: true });

  const r = spawnSync(SANDBOX_SCRIPT, cmd, {
    encoding: "utf-8",
    env: {
      ...process.env,
      SESSION_WORKSPACE: ws,
      SESSION_CLAUDE_DIR: cd,
      SESSION_TMP: tm,
    },
  });
  return {
    stdout: r.stdout ?? "",
    stderr: r.stderr ?? "",
    code: r.status ?? -1,
  };
}

function shQuote(s: string): string {
  return `'${s.replace(/'/g, "'\\''")}'`;
}

export async function* processMessageMock(
  sessionId: string,
  _agentId: string,
  _contentParts: ContentPart[],
  _modelConfig: unknown,
  agentConfig: AgentConfig,
  abortController?: AbortController,
): AsyncGenerator<SSEChunk> {
  const t0 = Date.now();
  yield { type: "query_started" };

  const scenario = agentConfig?.mock_scenario;
  const steps: MockStep[] = scenario?.steps ?? [
    { type: "chunk", content: "(no scenario provided)" },
  ];

  for (const step of steps) {
    if (abortController?.signal.aborted) {
      yield { type: "chunk", content: "[Cancelled by user]" };
      return;
    }

    switch (step.type) {
      case "chunk":
        if (step.delay_ms) await new Promise((r) => setTimeout(r, step.delay_ms));
        yield { type: "chunk", content: step.content };
        break;

      case "thinking":
        yield { type: "thinking", content: step.content };
        break;

      case "tool_use":
        yield {
          type: "tool_use",
          name: step.name,
          input: step.input ?? {},
          tool_use_id: `mock-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
        };
        break;

      case "write": {
        const r = runInSandbox(sessionId, [
          "sh", "-c",
          `mkdir -p "$(dirname ${shQuote(step.path)})" && printf %s ${shQuote(step.content)} > ${shQuote(step.path)}`,
        ]);
        if (r.code === 0) {
          yield { type: "chunk", content: `[wrote ${step.path}]` };
        } else {
          yield { type: "error", message: `write failed: ${r.stderr.trim()}` };
        }
        break;
      }

      case "read": {
        const r = runInSandbox(sessionId, ["cat", step.path]);
        yield {
          type: "chunk",
          content: r.code === 0 ? r.stdout : `[missing ${step.path}]`,
        };
        break;
      }

      case "ls": {
        const r = runInSandbox(sessionId, ["ls", "-la", step.path ?? "/workspace"]);
        yield { type: "chunk", content: r.stdout || r.stderr };
        break;
      }

      case "exec": {
        const r = runInSandbox(sessionId, step.cmd);
        yield { type: "chunk", content: r.stdout || r.stderr };
        break;
      }

      case "sleep":
        await new Promise((r) => setTimeout(r, step.ms));
        break;

      case "error":
        yield { type: "error", message: step.message };
        break;
    }
  }

  yield {
    type: "result",
    session_id: sessionId,
    duration_ms: Date.now() - t0,
    num_turns: 1,
    total_cost_usd: 0,
    usage: { input_tokens: 0, output_tokens: 0 },
  };
}
