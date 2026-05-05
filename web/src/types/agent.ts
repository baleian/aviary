export interface ModelConfig {
  /** LLM gateway model-name prefix (``anthropic``, ``ollama``, ``vllm``, …).
   *  Opaque to the frontend — sourced from `/api/inference/models`. */
  backend: string;
  model: string;
  max_output_tokens?: number;
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
  icon?: string;
  status: "active" | "disabled" | "deleted";
  created_at: string;
  updated_at: string;
}
