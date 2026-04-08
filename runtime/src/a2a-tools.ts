/**
 * Agent-to-Agent (A2A) calling tools.
 *
 * Runs a lightweight HTTP MCP server (JSON-RPC over HTTP POST) that exposes
 * one tool per accessible agent. Using HTTP type instead of SDK in-process type
 * ensures the CLI can always reconnect on session resume.
 */

import * as http from "node:http";

const SUPERVISOR_URL = process.env.AGENT_SUPERVISOR_URL || "";
const A2A_TIMEOUT = parseInt(process.env.A2A_CALL_TIMEOUT_SECONDS ?? "120", 10) * 1000;

const SUB_AGENT_PREFIX = `[SUB-AGENT MODE]
You are being invoked as a sub-agent by another agent to perform a specific task.
Guidelines:
- Focus exclusively on the task described in the message below.
- For large outputs (data, code, detailed results), write files to /home/shared/ and return a concise summary with file paths.
- The calling agent can read files from /home/shared/ to access your detailed results.
- Keep your response focused and actionable.

---

`;

export interface AccessibleAgent {
  slug: string;
  agent_id: string;
  name: string;
  description: string | null;
  instruction: string;
  model_config_data: Record<string, unknown>;
  tools: string[];
  mcp_servers: Record<string, unknown>;
}

export interface A2AContext {
  sessionId: string;
  userToken: string;
  credentials?: Record<string, string>;
}

/**
 * Call the target agent via the supervisor's SSE proxy.
 * Collects text chunks and returns the concatenated result.
 */
async function callSubAgent(
  agent: AccessibleAgent,
  message: string,
  ctx: A2AContext,
): Promise<string> {
  if (!SUPERVISOR_URL) {
    return "Error: AGENT_SUPERVISOR_URL not configured — cannot call sub-agents.";
  }

  const url = `${SUPERVISOR_URL}/v1/agents/${agent.agent_id}/sessions/${ctx.sessionId}/message`;

  const mcpConfig: Record<string, unknown> = {};
  if (agent.mcp_servers && typeof agent.mcp_servers === "object") {
    Object.assign(mcpConfig, agent.mcp_servers);
  }

  const body = {
    content: message,
    session_id: ctx.sessionId,
    model_config_data: agent.model_config_data,
    agent_config: {
      instruction: agent.instruction,
      tools: agent.tools,
      mcp_servers: mcpConfig,
      user_token: ctx.userToken,
      is_sub_agent: true,
      ...(ctx.credentials ? { credentials: ctx.credentials } : {}),
    },
  };

  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), A2A_TIMEOUT);

  try {
    const resp = await fetch(url, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Authorization: `Bearer ${ctx.userToken}`,
      },
      body: JSON.stringify(body),
      signal: controller.signal,
    });

    if (!resp.ok) {
      const errText = await resp.text();
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

// ── Lightweight MCP HTTP Server (JSON-RPC) ──────────────────

interface McpTool {
  name: string;
  description: string;
  inputSchema: Record<string, unknown>;
  handler: (args: Record<string, unknown>) => Promise<{ content: Array<{ type: string; text: string }> }>;
}

function buildToolDefinitions(agents: AccessibleAgent[], ctx: A2AContext): McpTool[] {
  return agents.map((agent) => ({
    name: `ask_${agent.slug}`,
    description: agent.description || `Call agent: ${agent.name}`,
    inputSchema: {
      type: "object",
      properties: {
        message: { type: "string", description: "The task or question to send to this agent" },
      },
      required: ["message"],
    },
    handler: async (args: Record<string, unknown>) => {
      const fullMessage = SUB_AGENT_PREFIX + String(args.message ?? "");
      const result = await callSubAgent(agent, fullMessage, ctx);
      return { content: [{ type: "text", text: result }] };
    },
  }));
}

function handleJsonRpc(
  tools: McpTool[],
  body: { jsonrpc: string; id: unknown; method: string; params?: Record<string, unknown> },
): Promise<Record<string, unknown>> {
  const { id, method, params } = body;

  if (method === "initialize") {
    return Promise.resolve({
      jsonrpc: "2.0",
      id,
      result: {
        protocolVersion: "2024-11-05",
        capabilities: { tools: { listChanged: false } },
        serverInfo: { name: "a2a", version: "1.0.0" },
      },
    });
  }

  if (method === "notifications/initialized") {
    // Notification — no response needed, but return empty to keep it simple
    return Promise.resolve({ jsonrpc: "2.0", id, result: {} });
  }

  if (method === "tools/list") {
    return Promise.resolve({
      jsonrpc: "2.0",
      id,
      result: {
        tools: tools.map((t) => ({
          name: t.name,
          description: t.description,
          inputSchema: t.inputSchema,
        })),
      },
    });
  }

  if (method === "tools/call") {
    const toolName = (params as any)?.name as string;
    const toolArgs = ((params as any)?.arguments ?? {}) as Record<string, unknown>;
    const tool = tools.find((t) => t.name === toolName);
    if (!tool) {
      return Promise.resolve({
        jsonrpc: "2.0",
        id,
        result: { content: [{ type: "text", text: `Unknown tool: ${toolName}` }], isError: true },
      });
    }
    return tool.handler(toolArgs).then((result) => ({
      jsonrpc: "2.0",
      id,
      result,
    }));
  }

  return Promise.resolve({
    jsonrpc: "2.0",
    id,
    error: { code: -32601, message: `Method not found: ${method}` },
  });
}

export interface A2AServer {
  /** URL to pass to mcpServers config (http://localhost:{port}/mcp) */
  url: string;
  /** Stop the HTTP server */
  close: () => void;
  /** Tool names for allowedTools */
  toolNames: string[];
}

/**
 * Start a local HTTP MCP server and return its URL.
 * The server lives for the duration of one processMessage call.
 */
export async function startA2AServer(
  agents: AccessibleAgent[],
  ctx: A2AContext,
): Promise<A2AServer> {
  const tools = buildToolDefinitions(agents, ctx);
  const toolNames = tools.map((t) => `mcp__a2a__${t.name}`);

  const server = http.createServer(async (req, res) => {
    // Handle POST for JSON-RPC
    if (req.method === "POST") {
      let body = "";
      for await (const chunk of req) body += chunk;

      try {
        const parsed = JSON.parse(body);
        const response = await handleJsonRpc(tools, parsed);
        res.writeHead(200, { "Content-Type": "application/json" });
        res.end(JSON.stringify(response));
      } catch (e: any) {
        res.writeHead(400, { "Content-Type": "application/json" });
        res.end(JSON.stringify({ jsonrpc: "2.0", id: null, error: { code: -32700, message: "Parse error" } }));
      }
      return;
    }

    // Handle DELETE for session termination (MCP spec)
    if (req.method === "DELETE") {
      res.writeHead(200);
      res.end();
      return;
    }

    res.writeHead(405);
    res.end();
  });

  // Listen on random available port
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const addr = server.address() as { port: number };

  return {
    url: `http://127.0.0.1:${addr.port}/mcp`,
    close: () => server.close(),
    toolNames,
  };
}
