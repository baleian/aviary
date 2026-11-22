// The resources of the API, as README.md describes them.

// The caller, from GET /v1/me. Without an identity provider the caller is
// nobody in particular, and only "admin" is set.
export interface Me {
  id?: string;
  subject?: string;
  username?: string;
  display_name?: string;
  groups?: string[];
  admin: boolean;
}

export interface Project {
  id: string;
  slug: string;
  // For display only, and empty if the project was given none.
  name: string;
  created_at: string;
}

export type Permission =
  | "project.view"
  | "agents.manage"
  | "connections.manage"
  | "sessions.run"
  | "sessions.view_all"
  | "sessions.control_all";

// A role and what it allows, the same in every project.
export interface Role {
  name: string;
  permissions: Permission[];
  updated_at?: string;
  updated_by?: string;
}

// A user or a group of the identity provider, and what it may do in a
// project: a role, or the permissions of the role "custom".
export interface Member {
  id: string;
  project_id: string;
  // A user who has not signed in yet has only a username.
  user?: { id?: string; username: string; display_name?: string };
  group?: string;
  role: string;
  permissions?: Permission[];
  created_at: string;
  updated_at: string;
  updated_by?: string;
}

// Where the key of a model provider is: kept by Aviary, or in a secret store
// of the project.
export type ApiKey =
  { stored: true } | { store: string; path: string; key: string };

export interface Provider {
  id: string;
  project_id: string;
  name: string;
  url: string;
  api_key?: ApiKey;
  created_at: string;
  updated_at: string;
}

// What came of asking a model provider for its models: whether its endpoint
// answered, with which status, and if not, why. The models are those that
// it listed, if it answered with a list.
export interface ProviderModels {
  reached: boolean;
  status?: number;
  models?: string[];
  reason?: string;
  duration_ms: number;
}

export interface Agent {
  id: string;
  project_id: string;
  slug: string;
  name: string;
  description: string;
  harness: string;
  // A provider of the project, by ID, and its name for the model.
  model: { provider: string; name: string };
  instructions?: string;
  // "ask" if the agent asks a person before it acts.
  approval?: string;
  created_at: string;
  updated_at: string;
  deleted_at?: string;
}

export interface Harness {
  name: string;
}

export interface SecretStore {
  id: string;
  project_id: string;
  name: string;
  // How the store is accessed, such as "kv-v2".
  kind: string;
  // What the kind needs to reach the store. The credential is not among it,
  // and is never returned.
  settings?: Record<string, unknown>;
  created_at: string;
  updated_at: string;
}

// What a tool call does, as its harness reports it.
export interface ToolCall {
  id?: string;
  title?: string;
  kind?: string;
  input?: unknown;
}

// A request of an agent to a person, to approve what it is about to do.
export interface ApprovalRequest {
  id: string;
  tool_call?: ToolCall;
  options: { id: string; name: string; kind: string }[];
}

export type TurnState =
  "queued" | "running" | "completed" | "failed" | "cancelled" | "expired";

// A turn: a message to a session, and what the agent did with it. A session
// carries its newest turn without the text.
export interface Turn {
  id: string;
  session_id: string;
  // The place of the turn in its session, from 1.
  seq: number;
  state: TurnState;
  text?: string;
  created_by?: string;
  created_at: string;
  // When the harness had the message.
  started_at?: string;
  ended_at?: string;
  cancel_requested_at?: string;
  // The place of the turn in the queue, while it waits there. The events of
  // the turn tell it, and its record does not have it.
  position?: number;
  failure?: { reason: string; message?: string };
  // Why the model stopped, for a turn that completed.
  stop_reason?: string;
  usage?: Usage;
  // The requests that wait for an answer, while the turn has not ended.
  approvals?: ApprovalRequest[];
  // What the agent said and did. The record of a turn has it once the turn
  // has ended. Until then the console assembles it from the events.
  items?: Item[];
}

// The tokens of a turn, as its harness reports them.
export interface Usage {
  input_tokens: number;
  output_tokens: number;
  thought_tokens?: number;
  cached_read_tokens?: number;
  cached_write_tokens?: number;
}

// A part of what an agent said and did in a turn.
export type Item =
  | { type: "message"; text: string }
  | { type: "thought"; text: string }
  | { type: "plan"; entries?: PlanEntry[] }
  | ({
      type: "tool_call";
      status?: string;
      output?: unknown;
      meta?: unknown;
    } & ToolCall)
  | ({
      type: "approval";
      // How the request ended: "selected" with the option that was chosen,
      // or otherwise without one.
      outcome?: string;
      option_id?: string;
      answered_by?: string;
    } & Partial<ApprovalRequest>);

export interface PlanEntry {
  content: string;
  status: "pending" | "in_progress" | "completed";
  priority?: string;
}

export interface Session {
  id: string;
  agent_id: string;
  title: string;
  created_by?: string;
  created_at: string;
  ended_at?: string;
  // Why the session ended, unless a caller ended it.
  end_reason?: "expired" | "agent_deleted";
  // Absent before the first turn and after the session has ended.
  sandbox?: "awake" | "asleep" | "lost";
  last_turn?: Turn;
}
