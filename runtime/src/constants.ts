// Mount points are coordinated with scripts/claude-sandbox.sh and the pool
// Deployment manifests at k8s/platform/pools/*.yaml — keep them in sync.

import * as path from "node:path";

// Host-side root: the shared agents-namespace workspace volume mounts here
// inside the pod (see k8s/platform/pools/_shared-workspace.yaml).
export const PLATFORM_ROOT = "/agent-platform";

// In-sandbox paths (bwrap remaps host paths to these — see claude-sandbox.sh).
export const WORKSPACE_ROOT = "/workspace";
export const SHARED_WORKSPACE_ROOT = "/workspace-shared";

// Cross-agent session workspace — all agents collaborating on the same session
// share this directory (file exchange, pip cache).
export function sessionWorkspace(sessionId: string): string {
  return path.join(PLATFORM_ROOT, "sessions", sessionId, "workspace");
}

// Per-(agent, session) CLI state overlay — used for claude-code resume.
export function agentClaudeDir(agentId: string, sessionId: string): string {
  return path.join(PLATFORM_ROOT, "agents", agentId, ".claude", sessionId);
}

// Per-(agent, session) Python venv — isolated so concurrent pip installs
// across agents within one session can't corrupt each other.
export function agentVenvDir(agentId: string, sessionId: string): string {
  return path.join(PLATFORM_ROOT, "agents", agentId, ".venvs", sessionId);
}

// Per-session tmp — ephemeral on the pod filesystem (not on the shared volume).
export function sessionTmp(sessionId: string): string {
  return `/tmp/${sessionId}`;
}
