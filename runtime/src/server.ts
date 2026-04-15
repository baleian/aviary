/**
 * Agent Runtime HTTP server — multi-tenant server that processes messages
 * via Claude Agent SDK (TypeScript).
 *
 * A single pod hosts sessions from many agents. Requests carry `agent_id` +
 * `session_id`; the session registry keys by the composite so concurrent
 * sessions never collide. Filesystem isolation between sessions/agents is
 * enforced by bubblewrap (see scripts/claude-sandbox.sh).
 */

import * as fs from "node:fs";
import express from "express";
import { SessionManager } from "./session-manager.js";
import { SHARED_WORKSPACE_ROOT, WORKSPACE_ROOT } from "./constants.js";
import { healthRouter, setReady } from "./health.js";
import { processMessage } from "./agent.js";

const app = express();
app.use(express.json({ limit: "50mb" }));
app.use(healthRouter);

const manager = new SessionManager();

// Track active AbortControllers per session for cancellation support
const activeAbortControllers = new Map<string, AbortController>();

// Startup
fs.mkdirSync(WORKSPACE_ROOT, { recursive: true });
fs.mkdirSync(SHARED_WORKSPACE_ROOT, { recursive: true });
setReady(true);

interface ContentPart {
  text?: string;
  attachments?: Array<{ type: string; media_type: string; data: string }>;
}

interface MessageRequestBody {
  content_parts: ContentPart[];
  session_id: string;
  agent_id: string;
  model_config_data?: Record<string, unknown> | null;
  agent_config: Record<string, unknown>;
  output_format?: { type: "json_schema"; schema: Record<string, unknown> };
}

app.post("/message", async (req, res) => {
  const body = req.body as MessageRequestBody;

  if (
    !body.content_parts?.length ||
    !body.session_id ||
    !body.agent_id ||
    !body.agent_config
  ) {
    res.status(400).json({
      error: "content_parts, session_id, agent_id, and agent_config are required",
    });
    return;
  }

  const entry = manager.getOrCreate(body.session_id, body.agent_id);

  // SSE headers
  res.setHeader("Content-Type", "text/event-stream");
  res.setHeader("Cache-Control", "no-cache");
  res.setHeader("X-Accel-Buffering", "no");
  res.setHeader("Connection", "keep-alive");
  res.flushHeaders();

  const release = await entry._lock.acquire();

  const abortController = new AbortController();
  activeAbortControllers.set(body.session_id, abortController);

  // Abort on client disconnect
  req.on("close", () => {
    if (!abortController.signal.aborted) {
      abortController.abort();
    }
  });

  try {
    for await (const chunk of processMessage(
      body.session_id,
      body.agent_id,
      body.content_parts,
      body.model_config_data as any,
      body.agent_config as any,
      abortController,
      body.output_format,
    )) {
      if (res.writableEnded || abortController.signal.aborted) break;
      res.write(`data: ${JSON.stringify(chunk)}\n\n`);
    }
  } finally {
    activeAbortControllers.delete(body.session_id);
    release();
    // Release slot immediately — workspace files are preserved for resume.
    // Next message will re-acquire a slot via getOrCreate().
    manager.remove(body.session_id, body.agent_id, false);
  }

  if (!res.writableEnded) {
    res.end();
  }
});

app.post("/abort/:sessionId", (req, res) => {
  const { sessionId } = req.params;
  const controller = activeAbortControllers.get(sessionId);
  if (!controller) {
    res.status(404).json({ error: "No active stream for this session" });
    return;
  }
  controller.abort();
  activeAbortControllers.delete(sessionId);
  res.json({ status: "aborted", session_id: sessionId });
});

app.get("/sessions", (_req, res) => {
  res.json({
    sessions: manager.listSessions(),
    active: manager.activeCount,
  });
});

function requireAgentId(req: express.Request, res: express.Response): string | null {
  const agentId = req.query.agent_id as string | undefined;
  if (!agentId) {
    res.status(400).json({ error: "agent_id query parameter is required" });
    return null;
  }
  return agentId;
}

app.delete("/sessions/:sessionId", (req, res) => {
  const agentId = requireAgentId(req, res);
  if (!agentId) return;
  const removed = manager.remove(req.params.sessionId, agentId, true);
  if (!removed) {
    res.status(404).json({ error: "session not found" });
    return;
  }
  res.json({ status: "removed" });
});

app.delete("/sessions/:sessionId/workspace", (req, res) => {
  const agentId = requireAgentId(req, res);
  if (!agentId) return;
  const { sessionId } = req.params;
  const claudeDir = `${WORKSPACE_ROOT}/.claude/${sessionId}`;

  // Also remove from manager if tracked (idempotent)
  manager.remove(sessionId, agentId, false);

  if (!fs.existsSync(claudeDir)) {
    res.json({ status: "not_found", session_id: sessionId });
    return;
  }
  fs.rmSync(claudeDir, { recursive: true, force: true });
  res.json({ status: "cleaned", session_id: sessionId });
});

const PORT = parseInt(process.env.PORT ?? "3000", 10);
const server = app.listen(PORT, "0.0.0.0", () => {
  console.log(`Aviary Runtime listening on :${PORT}`);
});

// Graceful shutdown.
// Pool Deployment (k8s/platform/pools/*.yaml) sets terminationGracePeriodSeconds=600;
// we wait up to 570s for in-flight Claude turns to finish, leaving a 30s buffer
// before K8s SIGKILLs. `setReady(false)` makes /ready return 503 so kube-proxy
// removes this pod from the Service's endpoints — new messages route elsewhere
// while we drain.
const GRACEFUL_SHUTDOWN_MS = 570_000;
for (const signal of ["SIGTERM", "SIGINT"] as const) {
  process.on(signal, async () => {
    console.log(`Received ${signal}, draining ${manager.activeCount} session(s)...`);
    setReady(false);
    server.close();
    await manager.gracefulShutdown(GRACEFUL_SHUTDOWN_MS);
    process.exit(0);
  });
}
