"use client";

import { Skeleton } from "@/components/ui/skeleton";
import { EmptyState } from "@/components/feedback/empty-state";
import { Store } from "@/components/icons";
import type { CatalogAgentSummary } from "@/types/catalog";
import { CatalogCard } from "./catalog-card";

interface CatalogGridProps {
  items: CatalogAgentSummary[];
  loading: boolean;
  searchActive: boolean;
  importedMap: Record<string, string>; // catalog_agent_id → local agent id
  onImport: (item: CatalogAgentSummary) => Promise<void>;
  emptyAction?: React.ReactNode;
}

export function CatalogGrid({
  items,
  loading,
  searchActive,
  importedMap,
  onImport,
  emptyAction,
}: CatalogGridProps) {
  if (loading) {
    return (
      <div className="grid gap-3 sm:grid-cols-2 md:grid-cols-3">
        {Array.from({ length: 6 }).map((_, i) => (
          <Skeleton key={i} className="h-48 rounded-lg" />
        ))}
      </div>
    );
  }

  if (items.length === 0) {
    return (
      <EmptyState
        icon={<Store size={24} strokeWidth={1.75} />}
        title={
          searchActive
            ? "No agents match your filters"
            : "No agents published yet"
        }
        description={
          searchActive
            ? "Try clearing a filter, or adjust the search query."
            : "Be the first to publish from your own workspace."
        }
        action={emptyAction}
      />
    );
  }

  return (
    <div className="grid gap-3 sm:grid-cols-2 md:grid-cols-3">
      {items.map((item) => (
        <CatalogCard
          key={item.id}
          item={item}
          importedAgentId={importedMap[item.id] ?? null}
          onImport={() => onImport(item)}
        />
      ))}
    </div>
  );
}
