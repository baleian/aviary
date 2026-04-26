/**
 * Agent-to-Agent (A2A) tools — in-process SDK tools under the `system` MCP server.
 * Each call POSTs the supervisor's /a2a endpoint, which forwards to the sub-agent.
 */

import * as crypto from "node:crypto";

import { tool, type SdkMcpToolDefinition } from "@anthropic-ai/claude-agent-sdk";
import { z } from "zod";

const SUPERVISOR_URL = process.env.AVIARY_SUPERVISOR_URL;
const A2A_TIMEOUT = parseInt(process.env.A2A_CALL_TIMEOUT_SECONDS ?? "1800", 10) * 1000;

const SUB_AGENT_PREFIX = `[SUB-AGENT MODE]
You are being invoked as a sub-agent by another agent to perform a specific task.
Guidelines:
- Focus exclusively on the task described in the message below.
- Your /workspace directory is shared with the calling agent. Write output files there if needed and return a summary with file paths.
- Keep your response focused and actionable.

---

`;

export interface AccessibleAgent {
  agent_id: string;
  slug: string;
  name: string;
  description: string | null;
  runtime_endpoint: string | null;
  model_config: Record<string, unknown>;
  instruction: string;
  tools: string[];
  mcp_servers: Record<string, unknown>;
}

export interface A2AContext {
  sessionId: string;
  userToken: string | undefined;
}

async function callSubAgent(
  agent: AccessibleAgent,
  message: string,
  ctx: A2AContext,
  parentToolUseId: string,
): Promise<string> {
  const url = `${SUPERVISOR_URL}/v1/sessions/${ctx.sessionId}/a2a`;

  const body = {
    parent_session_id: ctx.sessionId,
    parent_tool_use_id: parentToolUseId,
    agent_config: {
      agent_id: agent.agent_id,
      slug: agent.slug,
      name: agent.name,
      description: agent.description,
      runtime_endpoint: agent.runtime_endpoint,
      model_config: agent.model_config,
      instruction: agent.instruction,
      tools: agent.tools,
      mcp_servers: agent.mcp_servers,
    },
    content_parts: [{ text: message }],
  };

  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), A2A_TIMEOUT);

  const headers: Record<string, string> = { "Content-Type": "application/json" };
  if (ctx.userToken) headers.Authorization = `Bearer ${ctx.userToken}`;

  try {
    const resp = await fetch(url, {
      method: "POST",
      headers,
      body: JSON.stringify(body),
      signal: controller.signal,
    });

    if (!resp.ok) {
      const errText = await resp.text();
      console.error(`A2A call failed: ${agent.slug} HTTP ${resp.status}`, errText);
      return `Error calling agent @${agent.slug}: HTTP ${resp.status} — ${errText}`;
    }

    let result = "";
    const reader = resp.body?.getReader();
    if (!reader) return "Error: No response stream";

    const decoder = new TextDecoder();
    let buffer = "";

    while (true) {
      const { done, value } = await reader.read();
      if (done) break;

      buffer += decoder.decode(value, { stream: true });
      const lines = buffer.split("\n");
      buffer = lines.pop() || "";

      for (const line of lines) {
        if (!line.startsWith("data: ")) continue;
        try {
          const chunk = JSON.parse(line.slice(6));
          if (chunk.type === "chunk" && chunk.content) {
            result += chunk.content;
          }
        } catch {
          // skip malformed JSON
        }
      }
    }

    return result || "(No response from sub-agent)";
  } catch (e: any) {
    if (e.name === "AbortError") {
      return `Error: Sub-agent @${agent.slug} timed out after ${A2A_TIMEOUT / 1000}s`;
    }
    return `Error calling agent @${agent.slug}: ${e.message}`;
  } finally {
    clearTimeout(timeout);
  }
}

export interface A2AToolBundle {
  tools: SdkMcpToolDefinition<any>[];
  toolNames: string[];
  enqueueToolUseId: (mcpToolName: string, id: string) => void;
}

export function buildA2ATools(
  agents: AccessibleAgent[],
  ctx: A2AContext,
): A2AToolBundle {
  if (!SUPERVISOR_URL) {
    throw new Error(
      "AVIARY_SUPERVISOR_URL must be set when accessible_agents is provided",
    );
  }

  const queues = new Map<string, string[]>();
  const tools = agents.map((agent) => {
    const localName = `a2a_${agent.slug}`;
    queues.set(localName, []);
    return tool(
      localName,
      agent.description || `Call agent: ${agent.name}`,
      {
        message: z
          .string()
          .describe("The task or question to send to this agent"),
      },
      async (args) => {
        const queue = queues.get(localName);
        const parentToolUseId = queue?.shift() || `a2a_${crypto.randomUUID()}`;
        const result = await callSubAgent(
          agent,
          SUB_AGENT_PREFIX + String(args.message ?? ""),
          ctx,
          parentToolUseId,
        );
        return { content: [{ type: "text", text: result }] };
      },
    );
  });

  return {
    tools,
    toolNames: agents.map((a) => `mcp__system__a2a_${a.slug}`),
    enqueueToolUseId: (mcpToolName, id) => {
      const localName = mcpToolName.replace("mcp__system__", "");
      const queue = queues.get(localName);
      if (queue) queue.push(id);
    },
  };
}
