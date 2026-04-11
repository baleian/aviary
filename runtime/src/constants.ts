/**
 * Filesystem layout and runtime tunables shared across the agent runtime.
 *
 * These mount points are coordinated with the bubblewrap wrapper
 * (scripts/claude-sandbox.sh) and the K8s Deployment manifest in
 * agent-supervisor/app/manifests.py — keep them in sync.
 */

import * as path from "node:path";

/** Per-Pod PVC mount point — also where .claude/{sessionId} contexts live. */
export const WORKSPACE_ROOT = "/workspace";

/** hostPath mount shared across all agent Pods on the same node. */
export const SHARED_WORKSPACE_ROOT = "/workspace-shared";

export const DEFAULT_MAX_CONCURRENT_SESSIONS = 5;

/** Shared home directory for a session — same for all agents on the node (hostPath). */
export function sessionHome(sessionId: string): string {
  return path.join(SHARED_WORKSPACE_ROOT, sessionId);
}

/** Per-agent CLI context directory (PVC overlay, private to this Pod). */
export function sessionClaudeDir(sessionId: string): string {
  return path.join(WORKSPACE_ROOT, ".claude", sessionId);
}

/** Per-session /tmp — bind-mounted into the sandbox so other sessions can't see it. */
export function sessionTmp(sessionId: string): string {
  return `/tmp/${sessionId}`;
}
