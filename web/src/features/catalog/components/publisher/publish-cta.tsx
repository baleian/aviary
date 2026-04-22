"use client";

import { useCallback, useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Sparkles, RefreshCw } from "@/components/icons";
import { catalogApi } from "@/features/catalog/api/catalog-api";
import { PublishDialog } from "./publish-dialog";
import { PublishedStateChip } from "./published-state-chip";
import type { Agent } from "@/types/agent";
import type { DriftResponse } from "@/types/catalog";

interface PublishCtaProps {
  agent: Agent;
  currentUserId: string;
  onPublished?: () => void;
  /** Compact layout — no state chip on the left. Used inside the detail
   *  hero where space is tight. */
  compact?: boolean;
}

/**
 * Publish CTA + drift chip — the owner's working-copy-level catalog UI.
 *
 * Hides itself entirely when the agent isn't a publisher working copy
 * (imports have their own CTA; pure private agents also show "Draft" CTA
 * so users know publishing is possible).
 */
export function PublishCta({ agent, currentUserId, onPublished, compact }: PublishCtaProps) {
  const [drift, setDrift] = useState<DriftResponse | null>(null);
  const [dialogOpen, setDialogOpen] = useState(false);

  const fetchDrift = useCallback(async () => {
    try {
      const d = await catalogApi.drift(agent.id);
      setDrift(d);
    } catch {
      setDrift(null);
    }
  }, [agent.id]);

  useEffect(() => {
    // Skip drift when we know this is a consumer import (no drift meaning).
    if (agent.catalog_import_id) return;
    void fetchDrift();
  }, [agent.catalog_import_id, fetchDrift]);

  if (agent.catalog_import_id) return null;
  if (agent.owner_id !== currentUserId) return null;

  const linked = Boolean(agent.linked_catalog_agent_id);
  const neverPublished = !linked;
  const hasDrift = drift?.is_dirty === true;
  const inSync = drift?.is_dirty === false;

  const chipVariant = neverPublished
    ? "draft"
    : hasDrift
    ? "drift"
    : inSync
    ? "published"
    : "draft";
  const nextVersionLabel = `Publish v${(drift?.latest_version_number ?? 0) + 1}`;

  // Don't bother rendering if the working copy is clean and never published
  // — that's the "noise" case the plan calls out.
  const showChip = !neverPublished || hasDrift;

  return (
    <>
      <div className="flex items-center gap-3 flex-wrap">
        {!compact && showChip && (
          <PublishedStateChip
            variant={chipVariant}
            versionNumber={drift?.latest_version_number}
          />
        )}

        {hasDrift || neverPublished ? (
          <Button
            variant={hasDrift ? "cta" : "secondary"}
            size="sm"
            onClick={() => setDialogOpen(true)}
          >
            <Sparkles size={12} strokeWidth={2} />
            {nextVersionLabel}
          </Button>
        ) : inSync ? (
          <Button
            variant="ghost"
            size="sm"
            onClick={() => void fetchDrift()}
            aria-label="Refresh publish status"
          >
            <RefreshCw size={12} strokeWidth={2} />
            Recheck
          </Button>
        ) : null}
      </div>

      <PublishDialog
        open={dialogOpen}
        onClose={() => setDialogOpen(false)}
        agentId={agent.id}
        agentName={agent.name}
        drift={drift}
        onPublished={() => {
          void fetchDrift();
          onPublished?.();
        }}
      />
    </>
  );
}
