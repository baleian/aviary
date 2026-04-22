"use client";

import { useCallback, useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Sparkles, RefreshCw, Check } from "@/components/icons";
import { catalogApi } from "@/features/catalog/api/catalog-api";
import { PublishDialog } from "./publish-dialog";
import { PublishedStateChip } from "./published-state-chip";
import type { Agent } from "@/types/agent";
import type { DriftResponse } from "@/types/catalog";

interface PublishCtaProps {
  agent: Agent;
  currentUserId: string;
  onPublished?: () => void;
  /** Hides the state chip — useful when the hero already shows context. */
  compact?: boolean;
}

export function PublishCta({ agent, currentUserId, onPublished, compact }: PublishCtaProps) {
  const [drift, setDrift] = useState<DriftResponse | null>(null);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [flashPublished, setFlashPublished] = useState(false);

  const fetchDrift = useCallback(async () => {
    try {
      const d = await catalogApi.drift(agent.id);
      setDrift(d);
    } catch {
      setDrift(null);
    }
  }, [agent.id]);

  useEffect(() => {
    if (agent.catalog_import_id) return;
    void fetchDrift();
  }, [agent.catalog_import_id, fetchDrift]);

  if (agent.catalog_import_id) return null;
  if (agent.owner_id !== currentUserId) return null;

  // Linked-but-not-editor means the catalog is owned by someone else; hide.
  const editorOrUnlinked =
    !agent.linked_catalog_agent_id || agent.is_catalog_editor === true;
  if (!editorOrUnlinked) return null;

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

  const showChip = !neverPublished || hasDrift;

  return (
    <>
      <div className="flex items-center gap-3 flex-wrap">
        {flashPublished ? (
          <span
            role="status"
            className="inline-flex items-center gap-1.5 rounded-pill bg-success/12 text-success ring-1 ring-inset ring-success/25 px-3 py-1 type-caption-bold animate-fade-in-fast"
          >
            <Check size={12} strokeWidth={2.5} />
            Published v{drift?.latest_version_number ?? 1}
          </span>
        ) : (
          <>
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
          </>
        )}
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
          setFlashPublished(true);
          setTimeout(() => setFlashPublished(false), 4000);
        }}
      />
    </>
  );
}
