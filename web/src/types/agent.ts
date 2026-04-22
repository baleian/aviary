export interface ModelConfig {
  /** LiteLLM model-name prefix (``anthropic``, ``ollama``, ``vllm``, …).
   *  Opaque to the frontend — sourced from `/api/inference/models`. */
  backend: string;
  model: string;
  max_output_tokens?: number;
}

export interface McpServer {
  name: string;
  command: string;
  args: string[];
}

export interface Agent {
  id: string;
  name: string;
  slug: string;
  description?: string;
  owner_id: string;
  instruction: string;
  model_config: ModelConfig;
  tools: string[];
  mcp_servers: McpServer[];
  icon?: string;
  runtime_endpoint?: string | null;
  status: "active" | "disabled" | "deleted";
  /** Set to a CatalogAgent id when this agent is either a publisher's
   *  working copy (editable) or a consumer's imported read-only view. */
  linked_catalog_agent_id?: string | null;
  /** Non-null for consumer imports only. */
  catalog_import_id?: string | null;
  created_at: string;
  updated_at: string;
}
