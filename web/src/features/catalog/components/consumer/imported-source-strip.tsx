"use client";

import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ArrowRight, Store } from "@/components/icons";
import { catalogApi } from "@/features/catalog/api/catalog-api";
import { VersionDropdown } from "@/features/catalog/components/detail/version-dropdown";
import { routes } from "@/lib/constants/routes";
import type { Agent } from "@/types/agent";
import type { AgentVersionSummary, CatalogAgentDetail } from "@/types/catalog";

interface ImportedSourceStripProps {
  agent: Agent;
  onForkClicked: () => void;
  onMutated: () => void;
}

/**
 * Shown on the detail page of an imported agent. Surfaces the source
 * catalog entry, author, current pin, a Latest ⇄ Pinned version picker,
 * and a Fork CTA.
 */
export function ImportedSourceStrip({
  agent,
  onForkClicked,
  onMutated,
}: ImportedSourceStripProps) {
  const catalogId = agent.linked_catalog_agent_id;
  const [detail, setDetail] = useState<CatalogAgentDetail | null>(null);
  const [versions, setVersions] = useState<AgentVersionSummary[]>([]);
  const [pinnedVersionId, setPinnedVersionId] = useState<string | null>(
    agent.pinned_version_id ?? null,
  );
  const [loading, setLoading] = useState(true);
  const [mutating, setMutating] = useState(false);

  const load = useCallback(async () => {
    if (!catalogId) return;
    setLoading(true);
    try {
      const [d, v] = await Promise.all([
        catalogApi.detail(catalogId),
        catalogApi.versions(catalogId),
      ]);
      setDetail(d);
      setVersions(v.items);
      setPinnedVersionId(agent.pinned_version_id ?? null);
    } finally {
      setLoading(false);
    }
  }, [catalogId, agent.pinned_version_id]);

  useEffect(() => {
    void load();
  }, [load]);

  if (!catalogId) return null;

  const isDeprecated =
    detail?.unpublished_at != null ||
    (pinnedVersionId &&
      versions.find((v) => v.id === pinnedVersionId)?.unpublished_at != null);

  const onSelectVersion = async (versionId: string | null) => {
    if (mutating) return;
    setMutating(true);
    try {
      await catalogApi.patchImport(agent.id, versionId);
      setPinnedVersionId(versionId);
      onMutated();
      await load();
    } finally {
      setMutating(false);
    }
  };

  return (
    <div className="mb-6 flex flex-col gap-3 rounded-lg glass-pane p-4">
      <div className="flex flex-wrap items-center gap-2 type-caption">
        <Badge
          variant="neutral"
          className="flex items-center gap-1 border-aurora-coral/40 text-aurora-coral bg-aurora-c-soft"
        >
          <Store size={10} strokeWidth={2} />
          From Catalog
        </Badge>
        {loading ? (
          <span className="text-fg-disabled">Loading…</span>
        ) : detail ? (
          <>
            <span className="text-fg-muted">By {detail.owner_email}</span>
            <span className="text-fg-disabled">·</span>
            <Link
              href={routes.catalogAgent(detail.id)}
              className="inline-flex items-center gap-1 text-aurora-cyan hover:opacity-90"
            >
              View on catalog
              <ArrowRight size={10} strokeWidth={2} />
            </Link>
          </>
        ) : null}
      </div>

      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-2">
          <span className="type-caption text-fg-disabled">Version</span>
          {detail && versions.length > 0 ? (
            <VersionDropdown
              versions={versions}
              currentVersionId={detail.current_version_id}
              latestVersionId={detail.current_version_id}
              selectedVersionId={pinnedVersionId}
              onSelect={onSelectVersion}
            />
          ) : (
            <span className="type-caption text-fg-disabled">—</span>
          )}
          {mutating && (
            <span className="type-caption text-fg-disabled">Saving…</span>
          )}
        </div>

        <div className="flex items-center gap-2">
          <Button variant="ghost" size="sm" onClick={onForkClicked}>
            Fork
          </Button>
        </div>
      </div>

      {isDeprecated && (
        <div className="flex items-start gap-2 rounded-md border border-warning/30 bg-warning/[0.08] px-3 py-2 type-caption text-fg-secondary">
          <span className="mt-1 h-1.5 w-1.5 shrink-0 rounded-full bg-warning ring-2 ring-warning/20" />
          <span>
            This version is no longer live in the catalog. You can still
            chat with it, or fork to a private copy to keep editing.
          </span>
        </div>
      )}
    </div>
  );
}
