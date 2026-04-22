"use client";

import { AgentCard } from "./agent-card";
import { Skeleton } from "@/components/ui/skeleton";
import { EmptyState } from "@/components/feedback/empty-state";
import { Bot, Store } from "@/components/icons";
import type { Agent } from "@/types";

interface AgentGridProps {
  agents: Agent[];
  loading: boolean;
  emptyAction?: React.ReactNode;
  searchActive?: boolean;
}

/**
 * AgentGrid — loading/empty/populated + three sections:
 *   1. Private (mine) — default edit-enabled agents
 *   2. From Catalog — imported read-only agents (catalog_import_id set)
 *   3. Archived — soft-deleted
 */
export function AgentGrid({ agents, loading, emptyAction, searchActive }: AgentGridProps) {
  if (loading) {
    return (
      <div className="grid gap-3 sm:grid-cols-2 md:grid-cols-3">
        {[...Array(6)].map((_, i) => (
          <Skeleton key={i} className="h-44 rounded-lg" />
        ))}
      </div>
    );
  }

  if (agents.length === 0) {
    return (
      <EmptyState
        icon={<Bot size={20} strokeWidth={1.5} />}
        title={searchActive ? "No agents match your search" : "No agents yet"}
        description={searchActive ? "Try a different keyword." : "Create your first agent to get started."}
        action={!searchActive ? emptyAction : undefined}
      />
    );
  }

  const alive = agents.filter((a) => a.status !== "deleted");
  const privateAgents = alive.filter((a) => !a.catalog_import_id);
  const imported = alive.filter((a) => a.catalog_import_id);
  const deleted = agents.filter((a) => a.status === "deleted");

  return (
    <>
      {privateAgents.length > 0 && (
        <div className="grid gap-3 sm:grid-cols-2 md:grid-cols-3">
          {privateAgents.map((agent) => (
            <AgentCard key={agent.id} agent={agent} />
          ))}
        </div>
      )}

      {imported.length > 0 && (
        <div className={privateAgents.length > 0 ? "mt-10" : ""}>
          <h2 className="mb-4 flex items-center gap-2 type-caption-bold uppercase tracking-wide text-fg-muted">
            <Store size={12} strokeWidth={2} className="text-aurora-coral" />
            From Catalog
          </h2>
          <div className="grid gap-3 sm:grid-cols-2 md:grid-cols-3">
            {imported.map((agent) => (
              <AgentCard key={agent.id} agent={agent} fromCatalog />
            ))}
          </div>
        </div>
      )}

      {deleted.length > 0 && (
        <div className={privateAgents.length + imported.length > 0 ? "mt-10" : ""}>
          <h2 className="mb-4 type-small text-fg-disabled">Archived</h2>
          <div className="grid gap-3 sm:grid-cols-2 md:grid-cols-3">
            {deleted.map((agent) => (
              <AgentCard key={agent.id} agent={agent} deleted />
            ))}
          </div>
        </div>
      )}
    </>
  );
}
