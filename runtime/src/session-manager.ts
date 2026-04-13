import * as fs from "node:fs";
import {
  DEFAULT_MAX_CONCURRENT_SESSIONS,
  sessionClaudeDir,
  sessionTmp,
  sessionWorkspace,
} from "./constants.js";

export interface SessionEntry {
  sessionId: string;
  workspace: string;
  createdAt: number;
  _lock: { acquire(): Promise<() => void> };
}

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

const MAX_CONCURRENT_SESSIONS = parseInt(
  process.env.MAX_CONCURRENT_SESSIONS ?? String(DEFAULT_MAX_CONCURRENT_SESSIONS),
  10,
);

export class SessionManager {
  private _sessions = new Map<string, SessionEntry>();
  readonly maxSessions: number;

  constructor(maxSessions = MAX_CONCURRENT_SESSIONS) {
    this.maxSessions = maxSessions;
  }

  get activeCount(): number {
    return this._sessions.size;
  }

  get hasCapacity(): boolean {
    return this.activeCount < this.maxSessions;
  }

  getOrCreate(sessionId: string, agentId: string): SessionEntry {
    const key = `${sessionId}/${agentId}`;
    const existing = this._sessions.get(key);
    if (existing) return existing;

    if (!this.hasCapacity) {
      throw new Error(`Container at capacity (${this.maxSessions} sessions)`);
    }

    const workspace = sessionWorkspace(sessionId);
    fs.mkdirSync(workspace, { recursive: true });
    fs.mkdirSync(sessionClaudeDir(sessionId), { recursive: true });
    fs.mkdirSync(sessionTmp(sessionId), { recursive: true });

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
    if (cleanupFiles && fs.existsSync(entry.workspace)) {
      fs.rmSync(entry.workspace, { recursive: true, force: true });
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
    console.warn(`Shutdown timeout: ${this.activeCount} sessions still active`);
  }
}
