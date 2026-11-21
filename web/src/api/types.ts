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
