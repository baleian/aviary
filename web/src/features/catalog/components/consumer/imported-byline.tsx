"use client";

import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { Badge } from "@/components/ui/badge";
import { ArrowRight, Store } from "@/components/icons";
import { catalogApi } from "@/features/catalog/api/catalog-api";
import { VersionDropdown } from "@/features/catalog/components/detail/version-dropdown";
import { routes } from "@/lib/constants/routes";
import type { Agent } from "@/types/agent";
import type { AgentVersionSummary, CatalogAgentDetail } from "@/types/catalog";

interface ImportedBylineProps {
  agent: Agent;
  onMutated: () => void;
}

export function ImportedByline({ agent, onMutated }: ImportedBylineProps) {
  const catalogId = agent.linked_catalog_agent_id;
  const [detail, setDetail] = useState<CatalogAgentDetail | null>(null);
  const [versions, setVersions] = useState<AgentVersionSummary[]>([]);
  const [pinnedVersionId, setPinnedVersionId] = useState<string | null>(
    agent.pinned_version_id ?? null,
  );
  const [mutating, setMutating] = useState(false);

  const load = useCallback(async () => {
    if (!catalogId) return;
    const [d, v] = await Promise.all([
      catalogApi.detail(catalogId),
      catalogApi.versions(catalogId),
    ]);
    setDetail(d);
    setVersions(v.items);
    setPinnedVersionId(agent.pinned_version_id ?? null);
  }, [catalogId, agent.pinned_version_id]);

  useEffect(() => {
    void load();
  }, [load]);

  if (!catalogId) return null;

  const onSelectVersion = async (versionId: string | null) => {
    if (mutating) return;
    setMutating(true);
    try {
      await catalogApi.patchImport(agent.id, versionId);
      setPinnedVersionId(versionId);
      onMutated();
    } finally {
      setMutating(false);
    }
  };

  return (
    <>
      <Badge
        variant="neutral"
        className="flex items-center gap-1 border-aurora-coral/40 text-aurora-coral bg-aurora-c-soft"
      >
        <Store size={10} strokeWidth={2} />
        From Catalog
      </Badge>
      {detail && (
        <span className="truncate">By {detail.owner_email}</span>
      )}
      <span className="text-fg-disabled">·</span>
      {detail && versions.length > 0 ? (
        <VersionDropdown
          versions={versions}
          currentVersionId={detail.current_version_id ?? undefined}
          latestVersionId={detail.current_version_id ?? undefined}
          selectedVersionId={pinnedVersionId}
          onSelect={onSelectVersion}
        />
      ) : (
        <span className="text-fg-disabled">—</span>
      )}
      {detail && (
        <Link
          href={routes.catalogAgent(detail.id)}
          className="inline-flex items-center gap-0.5 text-aurora-cyan hover:opacity-90"
        >
          View source
          <ArrowRight size={10} strokeWidth={2} />
        </Link>
      )}
    </>
  );
}
