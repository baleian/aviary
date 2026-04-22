import type { Agent, ModelConfig } from "./agent";

export type CatalogCategory =
  | "coding"
  | "research"
  | "writing"
  | "productivity"
  | "marketing"
  | "data"
  | "ops"
  | "other";

export interface CatalogAgentSummary {
  id: string;
  slug: string;
  name: string;
  description?: string;
  icon?: string;
  category: string;
  owner_email: string;
  current_version_number: number;
  current_version_id: string;
  mcp_servers: string[];
  import_count: number;
  is_published: boolean;
  unpublished_at?: string | null;
  updated_at: string;
}

export interface McpToolBindingSnapshot {
  server_name: string;
  tool_name: string;
}

export interface AgentVersion {
  id: string;
  catalog_agent_id: string;
  version_number: number;
  name: string;
  description?: string;
  icon?: string;
  instruction: string;
  model_config: ModelConfig;
  tools: string[];
  mcp_servers: unknown[];
  mcp_tool_bindings: McpToolBindingSnapshot[];
  category: string;
  release_notes?: string | null;
  published_by: string;
  published_at: string;
  unpublished_at?: string | null;
}

export interface AgentVersionSummary {
  id: string;
  version_number: number;
  release_notes?: string | null;
  published_at: string;
  unpublished_at?: string | null;
}

export interface CatalogAgentDetail {
  id: string;
  slug: string;
  owner_id: string;
  owner_email: string;
  is_published: boolean;
  unpublished_at?: string | null;
  current_version_id?: string | null;
  current_version_number?: number | null;
  created_at: string;
  updated_at: string;
  import_count: number;
  version: AgentVersion;
  imported_as_agent_id?: string | null;
}

export interface CategoryFacet {
  name: string;
  count: number;
}

export interface ServerFacet {
  name: string;
  count: number;
}

export interface CatalogFacets {
  categories: CategoryFacet[];
  servers: ServerFacet[];
}

export interface CatalogListResponse {
  items: CatalogAgentSummary[];
  total: number;
}

export interface AgentVersionListResponse {
  items: AgentVersionSummary[];
  total: number;
}

export interface MyCatalogAgent {
  id: string;
  slug: string;
  name: string;
  description?: string;
  icon?: string;
  category: string;
  is_published: boolean;
  unpublished_at?: string | null;
  current_version_number?: number | null;
  linked_agent_id?: string | null;
  import_count: number;
  created_at: string;
  updated_at: string;
}

export interface MyCatalogListResponse {
  items: MyCatalogAgent[];
  total: number;
}

export interface ImportRequest {
  items: Array<{
    catalog_agent_id: string;
    pinned_version_id?: string | null;
  }>;
}

export interface ImportResponse {
  created: Agent[];
  already_imported: Agent[];
}

export interface DriftResponse {
  is_dirty: boolean;
  latest_version_number?: number | null;
  latest_version_id?: string | null;
  changed_fields: string[];
}
