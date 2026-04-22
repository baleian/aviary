import { http } from "@/lib/http";
import type {
  AgentVersionListResponse,
  CatalogAgentDetail,
  CatalogFacets,
  CatalogListResponse,
  DriftResponse,
  ImportRequest,
  ImportResponse,
  MyCatalogAgent,
  MyCatalogListResponse,
} from "@/types/catalog";
import type { Agent } from "@/types/agent";

export interface CatalogListParams {
  q?: string;
  category?: string[];
  mcp_server?: string[];
  sort?: "recent" | "popular" | "name";
  offset?: number;
  limit?: number;
}

function buildQuery(params: Record<string, unknown>): string {
  const usp = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === null || value === "") continue;
    if (Array.isArray(value)) {
      for (const v of value) if (v !== undefined && v !== null)
        usp.append(key, String(v));
    } else {
      usp.append(key, String(value));
    }
  }
  const s = usp.toString();
  return s ? `?${s}` : "";
}

export const catalogApi = {
  list(params: CatalogListParams = {}) {
    return http.get<CatalogListResponse>(
      `/catalog${buildQuery(params as Record<string, unknown>)}`,
    );
  },

  detail(id: string, versionId?: string) {
    const q = versionId ? `?v=${encodeURIComponent(versionId)}` : "";
    return http.get<CatalogAgentDetail>(`/catalog/${id}${q}`);
  },

  versions(id: string) {
    return http.get<AgentVersionListResponse>(`/catalog/${id}/versions`);
  },

  facets(params: { q?: string; category?: string[]; mcp_server?: string[] } = {}) {
    return http.get<CatalogFacets>(
      `/catalog/facets${buildQuery(params as Record<string, unknown>)}`,
    );
  },

  // ── Imports / fork ─────────────────────────────────────────────────
  listImports() {
    return http.get<{ items: Agent[]; total: number }>(`/catalog/imports`);
  },

  importAgents(body: ImportRequest) {
    return http.post<ImportResponse>(`/catalog/imports`, body);
  },

  getImport(agentId: string) {
    return http.get<{
      agent_id: string;
      catalog_agent_id: string;
      pinned_version_id: string | null;
      effective_version_id: string | null;
    }>(`/catalog/imports/${agentId}`);
  },

  patchImport(agentId: string, pinnedVersionId: string | null) {
    return http.patch<Agent>(`/catalog/imports/${agentId}`, {
      pinned_version_id: pinnedVersionId,
    });
  },

  removeImport(agentId: string) {
    return http.delete(`/catalog/imports/${agentId}`);
  },

  forkImport(agentId: string) {
    return http.post<Agent>(`/catalog/imports/${agentId}/fork`, {});
  },

  // ── Publisher ────────────────────────────────────────────────────
  mine(offset = 0, limit = 50) {
    return http.get<MyCatalogListResponse>(
      `/catalog/mine${buildQuery({ offset, limit })}`,
    );
  },

  myVersions(catalogAgentId: string) {
    return http.get<AgentVersionListResponse>(
      `/catalog/agents/${catalogAgentId}/versions`,
    );
  },

  rollback(catalogAgentId: string, versionId: string) {
    return http.post<MyCatalogAgent>(
      `/catalog/agents/${catalogAgentId}/rollback`,
      { version_id: versionId },
    );
  },

  unpublishCatalog(catalogAgentId: string) {
    return http.post<MyCatalogAgent>(
      `/catalog/agents/${catalogAgentId}/unpublish`,
      {},
    );
  },

  republishCatalog(catalogAgentId: string) {
    return http.post<MyCatalogAgent>(
      `/catalog/agents/${catalogAgentId}/republish`,
      {},
    );
  },

  unpublishVersion(versionId: string) {
    return http.post(`/catalog/versions/${versionId}/unpublish`, {});
  },

  deleteCatalogAgent(catalogAgentId: string) {
    return http.delete(`/catalog/agents/${catalogAgentId}`);
  },

  restore(catalogAgentId: string) {
    return http.post<Agent>(
      `/catalog/agents/${catalogAgentId}/restore`,
      {},
    );
  },

  publish(
    agentId: string,
    body: { category: string; release_notes?: string | null },
  ) {
    return http.post(`/agents/${agentId}/publish`, body);
  },

  drift(agentId: string) {
    return http.get<DriftResponse>(`/agents/${agentId}/drift`);
  },
};
