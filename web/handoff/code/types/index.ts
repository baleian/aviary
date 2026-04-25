/**
 * Domain types. One file per concept.
 * Copy the relevant blocks into src/types/*.ts.
 */

// =====================================================
// src/types/common.ts
// =====================================================

export type Tone = "blue" | "green" | "amber" | "pink" | "purple" | "teal" | "rose" | "slate";
export type AssetKind = "private" | "published" | "imported";
export type RunStatus = "queued" | "running" | "completed" | "failed" | "cancelled";

// =====================================================
// src/types/agent.ts
// =====================================================

export interface Agent {
  id: string;
  name: string;
  desc: string;
  kind: AssetKind;
  icon: string;                   // 2-letter identifier
  tone: Tone;
  model: string;                  // "Claude Sonnet 4.5" etc.
  tools: number;
  sessions: number;
  updated: string;                // ISO or humanized
  lastUsed: string;

  // Published-only
  version?: string;
  installs?: number;

  // Imported-only
  author?: string;
  hasUpdate?: boolean;
}

export interface AgentSession {
  id: string;
  agentId: string;
  title: string;
  when: string;
  pinned?: boolean;
  msgs: number;
  preview?: string;
}

export interface ChatMessage {
  id: string;
  sessionId: string;
  role: "user" | "assistant" | "system" | "tool";
  content: string;                // markdown for assistant/user, json for tool
  toolCalls?: ToolCall[];
  createdAt: string;
}

export interface ToolCall {
  id: string;
  name: string;
  args: Record<string, unknown>;
  result?: unknown;
  status: "pending" | "completed" | "failed";
}

// =====================================================
// src/types/workflow.ts
// =====================================================

export interface Workflow {
  id: string;
  name: string;
  desc: string;
  kind: AssetKind;
  category: string;
  tone: Tone;
  featured?: boolean;

  nodes: number;
  runs: number;
  status: "draft" | "deployed";
  lastRun: string;
  lastStatus: RunStatus;

  version?: string;
  installs?: number;
  author?: string;
}

export interface WorkflowRun {
  id: string;
  workflowId: string;
  status: RunStatus;
  trigger: string;
  duration: string;
  when: string;
}

// =====================================================
// src/types/marketplace.ts
// =====================================================

export interface MarketplaceItem {
  id: string;
  name: string;
  author: string;
  version: string;
  installs: string;              // "1.2k" — display already formatted
  rating: number;
  category: string;
  desc: string;
  tone: Tone;
  kind: "agent" | "workflow";
  imported?: boolean;
  newUpdate?: boolean;
}

// =====================================================
// src/types/notification.ts
// =====================================================

export interface Notification {
  id: string;
  kind: "chat_reply" | "workflow_complete" | "workflow_failed";
  tone: Tone;
  title: string;
  desc: string;
  when: string;
  unread?: boolean;
  agentId?: string;
  workflowId?: string;
}

// =====================================================
// src/types/workspace.ts
// =====================================================

export interface FileNode {
  name: string;
  kind: "dir" | "file";
  path: string;
  children?: FileNode[];
  expanded?: boolean;
  lang?: string;
  size?: string;
  mutated?: boolean;               // agent wrote to this file
}
