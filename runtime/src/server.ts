import * as fs from "node:fs";
import express from "express";
import { SessionManager } from "./session-manager.js";
import { SHARED_WORKSPACE_ROOT, WORKSPACE_ROOT } from "./constants.js";
import { healthRouter, setCapacityProbe, setReady } from "./health.js";
import { processMessage as processMessageReal } from "./agent.js";
import { processMessageMock } from "./mock-agent.js";

/**
 * Per-request dispatch: when `agent_config.mock_scenario` is present we run
 * the scripted mock (file ops still flow through the real bwrap sandbox),
 * otherwise we invoke claude-agent-sdk. No separate runtime mode.
 */
function pickProcessMessage(agentConfig: Record<string, unknown>) {
  return agentConfig?.mock_scenario ? processMessageMock : processMessageReal;
}

const app = express();
app.use(express.json({ limit: "50mb" }));
app.use(healthRouter);

const manager = new SessionManager();
const AGENT_ID = process.env.AGENT_ID;
if (!AGENT_ID) {
  throw new Error("Required environment variable AGENT_ID is not set");
}

const activeAbortControllers = new Map<string, AbortController>();

fs.mkdirSync(WORKSPACE_ROOT, { recursive: true });
fs.mkdirSync(SHARED_WORKSPACE_ROOT, { recursive: true });
setCapacityProbe(() => ({
  hasCapacity: manager.hasCapacity,
  activeCount: manager.activeCount,
}));
setReady(true);

interface ContentPart {
  text?: string;
  attachments?: Array<{ type: string; media_type: string; data: string }>;
}

interface MessageRequestBody {
  content_parts: ContentPart[];
  session_id: string;
  model_config_data?: Record<string, unknown> | null;
  agent_config: Record<string, unknown>;
}

app.post("/message", async (req, res) => {
  const body = req.body as MessageRequestBody;

  if (!body.content_parts?.length || !body.session_id || !body.agent_config) {
    res.status(400).json({ error: "content_parts, session_id, and agent_config are required" });
    return;
  }

  let entry;
  try {
    entry = manager.getOrCreate(body.session_id, AGENT_ID);
  } catch (e: any) {
    res.status(503).json({ error: e.message });
    return;
  }

  res.setHeader("Content-Type", "text/event-stream");
  res.setHeader("Cache-Control", "no-cache");
  res.setHeader("X-Accel-Buffering", "no");
  res.setHeader("Connection", "keep-alive");
  res.flushHeaders();

  const release = await entry._lock.acquire();
  const abortController = new AbortController();
  activeAbortControllers.set(body.session_id, abortController);

  req.on("close", () => {
    if (!abortController.signal.aborted) abortController.abort();
  });

  const process = pickProcessMessage(body.agent_config);

  try {
    for await (const chunk of process(
      body.session_id,
      AGENT_ID,
      body.content_parts,
      body.model_config_data as any,
      body.agent_config as any,
      abortController,
    )) {
      if (res.writableEnded || abortController.signal.aborted) break;
      res.write(`data: ${JSON.stringify(chunk)}\n\n`);
    }
  } finally {
    activeAbortControllers.delete(body.session_id);
    release();
    manager.remove(body.session_id, AGENT_ID, false);
  }

  if (!res.writableEnded) res.end();
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
    capacity: manager.maxSessions,
    active: manager.activeCount,
  });
});

app.delete("/sessions/:sessionId", (req, res) => {
  const removed = manager.remove(req.params.sessionId, AGENT_ID, true);
  if (!removed) {
    res.status(404).json({ error: "session not found" });
    return;
  }
  res.json({ status: "removed" });
});

app.delete("/sessions/:sessionId/workspace", (req, res) => {
  const { sessionId } = req.params;
  const claudeDir = `${WORKSPACE_ROOT}/.claude/${sessionId}`;
  manager.remove(sessionId, AGENT_ID, false);

  if (!fs.existsSync(claudeDir)) {
    res.json({ status: "not_found", session_id: sessionId });
    return;
  }
  fs.rmSync(claudeDir, { recursive: true, force: true });
  res.json({ status: "cleaned", session_id: sessionId });
});

app.get("/metrics", (_req, res) => {
  res.json({
    sessions_active: manager.activeCount,
    sessions_streaming: manager.activeCount,
    sessions_max: manager.maxSessions,
  });
});

app.post("/shutdown", (_req, res) => {
  setReady(false);
  res.json({ status: "shutting_down", streaming_sessions: manager.activeCount });
});

const PORT = parseInt(process.env.PORT ?? "3000", 10);
const server = app.listen(PORT, "0.0.0.0", () => {
  console.log(`Aviary Runtime listening on :${PORT}`);
});

for (const signal of ["SIGTERM", "SIGINT"] as const) {
  process.on(signal, async () => {
    console.log(`Received ${signal}, shutting down gracefully...`);
    setReady(false);
    server.close();
    await manager.gracefulShutdown(30_000);
    process.exit(0);
  });
}
