/**
 * App-wide route object. Single source of truth for which screen is shown.
 * See CLAUDE.md § "Route model" for why we don't use URL-driven state.
 */

export type PrimaryRoute =
  | "dashboard"
  | "agents"
  | "agent"
  | "workflows"
  | "workflow"
  | "marketplace"
  | "marketplace-item";

export type AgentTab = "chat" | "editor" | "runs" | "settings";
export type WorkflowTab = "overview" | "builder" | "runs" | "settings";
export type MarketplaceFilter = "all" | "agents" | "workflows" | "mine";

export interface Route {
  primary: PrimaryRoute;
  // Agents
  agentId?: string;
  agentTab?: AgentTab;
  // Workflows
  workflowId?: string;
  workflowTab?: WorkflowTab;
  // Marketplace
  marketplaceId?: string;
  marketplaceFilter?: MarketplaceFilter;
  // Cross-cutting
  sessionId?: string;
}

export const DEFAULT_ROUTE: Route = { primary: "dashboard" };

export function serializeRoute(r: Route): string {
  const p = new URLSearchParams();
  p.set("p", r.primary);
  if (r.agentId) p.set("agent", r.agentId);
  if (r.agentTab && r.agentTab !== "chat") p.set("at", r.agentTab);
  if (r.workflowId) p.set("workflow", r.workflowId);
  if (r.workflowTab && r.workflowTab !== "overview") p.set("wt", r.workflowTab);
  if (r.marketplaceId) p.set("m", r.marketplaceId);
  if (r.marketplaceFilter && r.marketplaceFilter !== "all") p.set("mf", r.marketplaceFilter);
  if (r.sessionId) p.set("s", r.sessionId);
  return p.toString();
}

export function deserializeRoute(qs: string): Route {
  const p = new URLSearchParams(qs);
  const primary = (p.get("p") || "dashboard") as PrimaryRoute;
  return {
    primary,
    agentId: p.get("agent") || undefined,
    agentTab: (p.get("at") as AgentTab) || (primary === "agent" ? "chat" : undefined),
    workflowId: p.get("workflow") || undefined,
    workflowTab: (p.get("wt") as WorkflowTab) || (primary === "workflow" ? "overview" : undefined),
    marketplaceId: p.get("m") || undefined,
    marketplaceFilter: (p.get("mf") as MarketplaceFilter) || undefined,
    sessionId: p.get("s") || undefined,
  };
}
