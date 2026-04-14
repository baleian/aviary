/**
 * Agent runner — claude-agent-sdk query with SSE streaming.
 *
 * MCP tool access runs through the platform MCP gateway (single HTTP endpoint
 * exposing all platform-provided servers, auth via the per-user OIDC token).
 */

import * as fs from "node:fs";
import * as path from "node:path";

import { query, type SDKMessage } from "@anthropic-ai/claude-agent-sdk";
import { WORKSPACE_ROOT, sessionClaudeDir, sessionTmp, sessionVenvDir, sessionWorkspace } from "./constants.js";

const LLM_GATEWAY_URL = process.env.LLM_GATEWAY_URL ?? (() => { throw new Error("LLM_GATEWAY_URL is required"); })();
const LLM_GATEWAY_API_KEY = process.env.LLM_GATEWAY_API_KEY ?? (() => { throw new Error("LLM_GATEWAY_API_KEY is required"); })();
const MCP_GATEWAY_URL = process.env.MCP_GATEWAY_URL;
const CLAUDE_CLI_PATH = "/usr/local/bin/claude";

const MODEL_TIER_KEYS = [
  "ANTHROPIC_MODEL",
  "ANTHROPIC_SMALL_FAST_MODEL",
  "ANTHROPIC_DEFAULT_HAIKU_MODEL",
  "ANTHROPIC_DEFAULT_SONNET_MODEL",
  "ANTHROPIC_DEFAULT_OPUS_MODEL",
  "CLAUDE_CODE_SUBAGENT_MODEL",
] as const;

const PASSTHROUGH_KEYS = ["PATH", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY"] as const;

interface AgentConfig {
  instruction?: string;
  tools?: string[];
  credentials?: Record<string, string>;
}

interface UserContext {
  external_id?: string;
  token?: string;
}

interface ModelConfig {
  model?: string;
  backend?: string;
  max_output_tokens?: number;
}

interface ContentPart {
  text?: string;
  attachments?: Array<{ type: string; media_type: string; data: string }>;
}

export interface SSEChunk {
  type: "chunk" | "tool_use" | "tool_result" | "tool_progress" | "result" | "thinking" | "query_started" | "error";
  content?: string;
  name?: string;
  input?: unknown;
  tool_use_id?: string;
  is_error?: boolean;
  parent_tool_use_id?: string | null;
  tool_name?: string;
  elapsed_time_seconds?: number;
  message?: string;
  session_id?: string;
  duration_ms?: number;
  num_turns?: number;
  total_cost_usd?: number;
  usage?: Record<string, unknown>;
}

function hasSessionHistory(claudeDir: string, sessionId: string): boolean {
  const projectsDir = path.join(claudeDir, "projects");
  if (!fs.existsSync(projectsDir)) return false;
  try {
    for (const d of fs.readdirSync(projectsDir)) {
      if (fs.existsSync(path.join(projectsDir, d, `${sessionId}.jsonl`))) return true;
    }
  } catch { /* not readable */ }
  return false;
}

function buildMessageContent(parts: ContentPart[]): string | Array<Record<string, unknown>> {
  const hasAttachments = parts.some((p) => p.attachments?.length);
  if (!hasAttachments && parts.length === 1 && parts[0].text) return parts[0].text;

  const blocks: Array<Record<string, unknown>> = [];
  for (const part of parts) {
    if (part.attachments?.length) {
      for (const a of part.attachments.filter((a) => a.type === "image")) {
        blocks.push({ type: "image", source: { type: "base64", media_type: a.media_type, data: a.data } });
      }
    }
    if (part.text) blocks.push({ type: "text", text: part.text });
  }
  return blocks;
}

export async function* processMessage(
  sessionId: string,
  agentId: string,
  contentParts: ContentPart[],
  modelConfig: ModelConfig | null | undefined,
  agentConfig: AgentConfig,
  user: UserContext,
  abortController?: AbortController,
): AsyncGenerator<SSEChunk> {
  const workspace = sessionWorkspace(sessionId);
  const claudeDir = sessionClaudeDir(sessionId);
  const tmpDir = sessionTmp(sessionId);
  const venvDir = sessionVenvDir(sessionId);
  fs.mkdirSync(workspace, { recursive: true });
  fs.mkdirSync(claudeDir, { recursive: true });
  fs.mkdirSync(tmpDir, { recursive: true });
  fs.mkdirSync(path.dirname(venvDir), { recursive: true });

  const mc: ModelConfig = modelConfig ?? {};
  if (!mc.model || !mc.backend) {
    throw new Error("model_config.model and model_config.backend are required");
  }

  const resolvedModel = mc.model.includes("/") ? mc.model : `${mc.backend}/${mc.model}`;
  const canResume = hasSessionHistory(claudeDir, sessionId);

  const env: Record<string, string> = {
    ANTHROPIC_BASE_URL: LLM_GATEWAY_URL,
    ANTHROPIC_API_KEY: LLM_GATEWAY_API_KEY,
    ...(user.token
      ? { ANTHROPIC_CUSTOM_HEADERS: `X-Aviary-User-Token: ${user.token}` }
      : {}),
    SESSION_WORKSPACE: workspace,
    SESSION_CLAUDE_DIR: claudeDir,
    SESSION_TMP: tmpDir,
    SESSION_VENV_DIR: venvDir,
    CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: "1",
    CLAUDE_CODE_MAX_RETRIES: "2",
    ...(mc.max_output_tokens != null
      ? { CLAUDE_CODE_MAX_OUTPUT_TOKENS: String(mc.max_output_tokens) }
      : {}),
    ...Object.fromEntries(MODEL_TIER_KEYS.map((k) => [k, resolvedModel])),
    ...Object.fromEntries(
      PASSTHROUGH_KEYS.filter((k) => process.env[k]).map((k) => [k, process.env[k]!]),
    ),
    // GitHub token — enables git/gh CLI authentication inside the sandbox.
    // GH_TOKEN is used by gh CLI, GITHUB_TOKEN by git credential helper.
    ...(agentConfig.credentials?.github_token
      ? {
          GITHUB_TOKEN: agentConfig.credentials.github_token,
          GH_TOKEN: agentConfig.credentials.github_token,
          GIT_CONFIG_COUNT: "1",
          GIT_CONFIG_KEY_0: "credential.https://github.com.helper",
          GIT_CONFIG_VALUE_0: "/app/scripts/git-credential-github.sh",
        }
      : {}),
  };

  const systemPrompt = {
    type: "preset" as const,
    preset: "claude_code" as const,
    append: agentConfig.instruction || "",
  };

  const mcpServers: Record<string, unknown> = {};
  if (MCP_GATEWAY_URL && user.token) {
    mcpServers["aviary"] = {
      type: "http",
      url: `${MCP_GATEWAY_URL}/mcp/v1/${agentId}`,
      headers: { Authorization: `Bearer ${user.token}` },
    };
  }

  const options: Record<string, unknown> = {
    model: resolvedModel,
    systemPrompt,
    settingSources: ["user"],
    cwd: WORKSPACE_ROOT,
    pathToClaudeCodeExecutable: CLAUDE_CLI_PATH,
    permissionMode: "bypassPermissions" as const,
    allowedTools: agentConfig.tools?.length ? agentConfig.tools : undefined,
    disallowedTools: ["WebSearch"],
    mcpServers: Object.keys(mcpServers).length > 0 ? mcpServers : undefined,
    env,
    includePartialMessages: true,
    ...(canResume ? {} : { extraArgs: { "session-id": sessionId } }),
    ...(canResume ? { resume: sessionId } : {}),
    ...(abortController ? { abortController } : {}),
  };

  async function* promptGenerator() {
    yield {
      type: "user" as const,
      message: { role: "user" as const, content: buildMessageContent(contentParts) },
      parent_tool_use_id: null,
      session_id: sessionId,
    };
  }

  let fullResponse = "";
  let emittedTextLen = 0;
  let emittedThinkingLen = 0;
  const emittedToolIds = new Set<string>();
  let hasStreamDeltas = false;

  try {
    const stream = query({ prompt: promptGenerator(), options });
    yield { type: "query_started" } as SSEChunk;

    for await (const message of stream) {
      const msg = message as SDKMessage & Record<string, any>;

      if (msg.type === "stream_event") {
        const event = msg.event as Record<string, any>;
        if (event.type === "content_block_delta" && event.delta) {
          if (event.delta.type === "text_delta" && event.delta.text) {
            hasStreamDeltas = true;
            const delta = event.delta.text as string;
            emittedTextLen += delta.length;
            fullResponse += delta;
            yield { type: "chunk", content: delta };
          } else if (event.delta.type === "thinking_delta" && event.delta.thinking) {
            hasStreamDeltas = true;
            const delta = event.delta.thinking as string;
            emittedThinkingLen += delta.length;
            yield { type: "thinking", content: delta };
          }
        }
      } else if (msg.type === "assistant" && msg.message?.content) {
        const parentId = msg.parent_tool_use_id ?? null;
        for (const block of msg.message.content) {
          if (block.type === "thinking" && !hasStreamDeltas) {
            const thinking = (block.thinking ?? "") as string;
            if (thinking.length < emittedThinkingLen) emittedThinkingLen = 0;
            if (thinking.length > emittedThinkingLen) {
              yield { type: "thinking", content: thinking.slice(emittedThinkingLen) };
              emittedThinkingLen = thinking.length;
            }
          } else if (block.type === "text" && !hasStreamDeltas) {
            const text = block.text as string;
            if (text.length < emittedTextLen) emittedTextLen = 0;
            if (text.length > emittedTextLen) {
              const delta = text.slice(emittedTextLen);
              emittedTextLen = text.length;
              fullResponse += delta;
              yield { type: "chunk", content: delta };
            }
          } else if (block.type === "tool_use" && !emittedToolIds.has(block.id)) {
            emittedToolIds.add(block.id);
            yield {
              type: "tool_use",
              name: block.name,
              input: block.input,
              tool_use_id: block.id,
              ...(parentId ? { parent_tool_use_id: parentId } : {}),
            };
          }
        }
      } else if (msg.type === "user" && msg.message?.content) {
        emittedTextLen = 0;
        emittedThinkingLen = 0;
        const parentId = msg.parent_tool_use_id ?? null;
        const content = msg.message.content;
        if (Array.isArray(content)) {
          for (const block of content) {
            if (block.type === "tool_result") {
              yield {
                type: "tool_result",
                tool_use_id: block.tool_use_id,
                content: typeof block.content === "string" ? block.content : JSON.stringify(block.content ?? ""),
                ...(block.is_error ? { is_error: true } : {}),
                ...(parentId ? { parent_tool_use_id: parentId } : {}),
              };
            }
          }
        }
      } else if (msg.type === "tool_progress") {
        yield {
          type: "tool_progress",
          tool_use_id: msg.tool_use_id,
          tool_name: msg.tool_name,
          parent_tool_use_id: msg.parent_tool_use_id,
          elapsed_time_seconds: msg.elapsed_time_seconds,
        };
      } else if (msg.type === "result") {
        if (msg.result && !fullResponse) {
          fullResponse = msg.result;
          yield { type: "chunk", content: msg.result };
        }
        yield {
          type: "result",
          session_id: msg.session_id,
          duration_ms: msg.duration_ms,
          num_turns: msg.num_turns,
          total_cost_usd: msg.total_cost_usd,
          usage: msg.usage,
        };
      }
    }
  } catch (e) {
    if (abortController?.signal.aborted) {
      yield { type: "chunk", content: "[Cancelled by user]" };
      return;
    }
    console.error(`[agent ${agentId}/${sessionId}] SDK query error:`, e);
    yield { type: "error", message: e instanceof Error ? e.message : String(e) };
  }
}
