/**
 * Internal form types for the agent create/edit flow.
 */

export interface AgentFormData {
  name: string;
  slug: string;
  description: string;
  instruction: string;
  model_config: {
    backend: string;
    model: string;
    max_output_tokens: number;
  };
  tools: string[];
  mcp_tool_ids: string[];
  visibility: string;
  category: string;
}

export const DEFAULT_AGENT_FORM_DATA: AgentFormData = {
  name: "",
  slug: "",
  description: "",
  instruction: "",
  model_config: {
    backend: "claude",
    model: "",
    max_output_tokens: 4000,
  },
  tools: [],
  mcp_tool_ids: [],
  visibility: "private",
  category: "",
};
