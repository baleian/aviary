/**
 * Session manager for multi-session runtime Pod.
 *
 * Tracks active sessions, enforces concurrency limits, and provides
 * per-session message serialization via mutex locks.
 */

import * as fs from "node:fs";
import * as path from "node:path";

export const WORKSPACE_ROOT = "/workspace/sessions";
const MAX_CONCURRENT_SESSIONS = parseInt(
  process.env.MAX_CONCURRENT_SESSIONS ?? "10",
  10,
);

export enum SessionState {
  IDLE = "idle",
  STREAMING = "streaming",
  ERROR = "error",
}

export interface SessionEntry {
  sessionId: string;
  state: SessionState;
  workspace: string;
  createdAt: number;
  lastActiveAt: number;
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
  readonly maxSessions: number;

  constructor(maxSessions = MAX_CONCURRENT_SESSIONS) {
    this.maxSessions = maxSessions;
  }

  get activeCount(): number {
    return this._sessions.size;
  }

  get streamingCount(): number {
    let count = 0;
    for (const s of this._sessions.values()) {
      if (s.state === SessionState.STREAMING) count++;
    }
    return count;
  }

  get hasCapacity(): boolean {
    return this.activeCount < this.maxSessions;
  }

  getOrCreate(sessionId: string): SessionEntry {
    const existing = this._sessions.get(sessionId);
    if (existing) return existing;

    if (!this.hasCapacity) {
      throw new Error(`Pod at capacity (${this.maxSessions} sessions)`);
    }

    const workspace = path.join(WORKSPACE_ROOT, sessionId);
    fs.mkdirSync(workspace, { recursive: true });

    const entry: SessionEntry = {
      sessionId,
      state: SessionState.IDLE,
      workspace,
      createdAt: Date.now() / 1000,
      lastActiveAt: Date.now() / 1000,
      _lock: createMutex(),
    };
    this._sessions.set(sessionId, entry);
    return entry;
  }

  get(sessionId: string): SessionEntry | undefined {
    return this._sessions.get(sessionId);
  }

  remove(sessionId: string, cleanupFiles = true): boolean {
    const entry = this._sessions.get(sessionId);
    if (!entry) return false;
    this._sessions.delete(sessionId);
    if (cleanupFiles && fs.existsSync(entry.workspace)) {
      fs.rmSync(entry.workspace, { recursive: true, force: true });
    }
    return true;
  }

  listSessions(): Array<{
    session_id: string;
    state: string;
    created_at: number;
    last_active_at: number;
  }> {
    return Array.from(this._sessions.values()).map((e) => ({
      session_id: e.sessionId,
      state: e.state,
      created_at: e.createdAt,
      last_active_at: e.lastActiveAt,
    }));
  }

  async gracefulShutdown(timeout = 30_000): Promise<void> {
    const deadline = Date.now() + timeout;
    while (Date.now() < deadline) {
      if (this.streamingCount === 0) return;
      await new Promise((r) => setTimeout(r, 500));
    }
    console.warn(
      `Shutdown timeout: ${this.streamingCount} sessions still streaming`,
    );
  }
}
