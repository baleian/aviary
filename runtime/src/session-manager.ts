/**
 * Session registry + per-session mutex for the agent runtime.
 *
 * Keyed by `${sessionId}/${agentId}` so a single pod can host sessions from
 * many agents concurrently. No hard concurrency cap — pool-level scaling is
 * owned by infra (KEDA on the pool Deployment); transient overshoot during
 * scale-up is accepted.
 */

import * as fs from "node:fs";
import * as path from "node:path";
import {
  PLATFORM_ROOT,
  SHARED_WORKSPACE_ROOT,
  WORKSPACE_ROOT,
  agentClaudeDir,
  agentVenvDir,
  sessionTmp,
  sessionWorkspace,
} from "./constants.js";

export { PLATFORM_ROOT, WORKSPACE_ROOT, SHARED_WORKSPACE_ROOT };

export interface SessionEntry {
  sessionId: string;
  workspace: string;
  createdAt: number;
  /** Simple mutex: resolves when the lock is released. */
  _lock: {
    acquire(): Promise<() => void>;
  };
}

/** Creates a simple async mutex. */
function createMutex() {
  let _queue: Array<() => void> = [];
  let _locked = false;

  return {
    acquire(): Promise<() => void> {
      return new Promise<() => void>((resolve) => {
        const tryAcquire = () => {
          if (!_locked) {
            _locked = true;
            resolve(() => {
              _locked = false;
              const next = _queue.shift();
              if (next) next();
            });
          } else {
            _queue.push(tryAcquire);
          }
        };
        tryAcquire();
      });
    },
  };
}

export class SessionManager {
  private _sessions = new Map<string, SessionEntry>();

  get activeCount(): number {
    return this._sessions.size;
  }

  getOrCreate(sessionId: string, agentId: string): SessionEntry {
    const key = `${sessionId}/${agentId}`;
    const existing = this._sessions.get(key);
    if (existing) return existing;

    const workspace = sessionWorkspace(sessionId);
    fs.mkdirSync(workspace, { recursive: true });
    fs.mkdirSync(agentClaudeDir(agentId, sessionId), { recursive: true });
    fs.mkdirSync(sessionTmp(sessionId), { recursive: true });
    // venv parent only — claude-sandbox.sh creates the venv itself.
    fs.mkdirSync(path.dirname(agentVenvDir(agentId, sessionId)), { recursive: true });

    const entry: SessionEntry = {
      sessionId,
      workspace,
      createdAt: Date.now() / 1000,
      _lock: createMutex(),
    };
    this._sessions.set(key, entry);
    return entry;
  }

  get(sessionId: string, agentId: string): SessionEntry | undefined {
    return this._sessions.get(`${sessionId}/${agentId}`);
  }

  remove(sessionId: string, agentId: string, cleanupFiles = false): boolean {
    const key = `${sessionId}/${agentId}`;
    const entry = this._sessions.get(key);
    if (!entry) return false;
    this._sessions.delete(key);
    if (cleanupFiles) {
      // Remove both the shared session workspace and this agent's overlays.
      for (const p of [
        entry.workspace,
        agentClaudeDir(agentId, sessionId),
        agentVenvDir(agentId, sessionId),
      ]) {
        if (fs.existsSync(p)) fs.rmSync(p, { recursive: true, force: true });
      }
    }
    return true;
  }

  listSessions(): Array<{ session_id: string; created_at: number }> {
    return Array.from(this._sessions.values()).map((e) => ({
      session_id: e.sessionId,
      created_at: e.createdAt,
    }));
  }

  async gracefulShutdown(timeout = 30_000): Promise<void> {
    const deadline = Date.now() + timeout;
    while (Date.now() < deadline) {
      if (this.activeCount === 0) return;
      await new Promise((r) => setTimeout(r, 500));
    }
    console.warn(
      `Shutdown timeout: ${this.activeCount} sessions still active`,
    );
  }
}
