export const routes = {
  home: "/",
  login: "/login",
  authCallback: "/auth/callback",
  agents: "/agents",
  agentNew: "/agents/new",
  agent: (id: string) => `/agents/${id}`,
  agentEdit: (id: string) => `/agents/${id}/edit`,
  agentSessions: (id: string) => `/agents/${id}/sessions`,
  session: (id: string) => `/sessions/${id}`,
  catalog: "/catalog",
  catalogAgent: (id: string) => `/catalog/${id}`,
  catalogAgentAtVersion: (id: string, v: string) => `/catalog/${id}?v=${v}`,
  catalogMine: "/catalog/mine",
  workflows: "/workflows",
  workflowNew: "/workflows/new",
  workflow: (id: string) => `/workflows/${id}`,
  workflowRuns: (id: string) => `/workflows/${id}/runs`,
  /** Builder deep-link: open workflow at a specific version (and run,
   *  if any). `versionId` may be the "draft" sentinel for draft runs. */
  workflowAtVersion: (id: string, versionId: string, runId?: string) =>
    runId
      ? `/workflows/${id}?runId=${runId}&versionId=${versionId}`
      : `/workflows/${id}?versionId=${versionId}`,
} as const;
