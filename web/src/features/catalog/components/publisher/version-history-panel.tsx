"use client";

import { useCallback, useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { History, MoreHorizontal } from "@/components/icons";
import { catalogApi } from "@/features/catalog/api/catalog-api";
import { cn } from "@/lib/utils";
import type { AgentVersionSummary, MyCatalogAgent } from "@/types/catalog";

interface VersionHistoryPanelProps {
  /** The linked catalog agent id from the agents row. Null → nothing to show. */
  catalogAgentId: string | null;
}

export function VersionHistoryPanel({ catalogAgentId }: VersionHistoryPanelProps) {
  const [entry, setEntry] = useState<MyCatalogAgent | null>(null);
  const [versions, setVersions] = useState<AgentVersionSummary[]>([]);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [openId, setOpenId] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [showAll, setShowAll] = useState(false);

  const load = useCallback(async () => {
    if (!catalogAgentId) return;
    setLoading(true);
    try {
      const [mine, v] = await Promise.all([
        catalogApi.mine(),
        catalogApi.myVersions(catalogAgentId),
      ]);
      const hit = mine.items.find((i) => i.id === catalogAgentId) ?? null;
      setEntry(hit);
      setVersions(v.items);
    } finally {
      setLoading(false);
    }
  }, [catalogAgentId]);

  useEffect(() => {
    void load();
  }, [load]);

  if (!catalogAgentId) return null;

  const handle = async (fn: () => Promise<unknown>, confirmText?: string) => {
    if (confirmText && !confirm(confirmText)) return;
    setBusy(true);
    setError(null);
    try {
      await fn();
      await load();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Action failed");
    } finally {
      setBusy(false);
      setOpenId(null);
    }
  };

  const currentVersionId = entry?.current_version_number
    ? versions.find((v) => v.version_number === entry.current_version_number)?.id
    : null;

  const shown = showAll ? versions : versions.slice(0, 5);

  return (
    <section className="mt-10 rounded-lg glass-pane p-5">
      <header className="mb-3 flex items-center gap-2">
        <History size={14} strokeWidth={2} className="text-fg-muted" />
        <h2 className="type-caption-bold uppercase tracking-wide text-fg-muted">
          Version history
        </h2>
      </header>

      {loading ? (
        <p className="type-caption text-fg-disabled">Loading…</p>
      ) : versions.length === 0 ? (
        <p className="type-caption text-fg-disabled">
          No versions yet — publish this agent to create v1.
        </p>
      ) : (
        <ol className="relative flex flex-col gap-0.5 pl-4">
          <div
            aria-hidden
            className="pointer-events-none absolute left-1.5 top-1 bottom-1 w-px bg-white/[0.08]"
          />
          {shown.map((v) => {
            const isLatest = v.id === currentVersionId;
            const menuOpen = openId === v.id;
            return (
              <li
                key={v.id}
                className={cn(
                  "relative flex items-start gap-3 rounded-sm py-2 pl-4 pr-2",
                  "transition-colors hover:bg-white/[0.03]",
                )}
              >
                <span
                  aria-hidden
                  className={cn(
                    "absolute left-0 top-3 h-2 w-2 rounded-full ring-2",
                    isLatest
                      ? "bg-aurora-cyan ring-aurora-cyan/30"
                      : v.unpublished_at
                      ? "bg-fg-disabled ring-white/[0.08]"
                      : "bg-fg-muted ring-white/[0.08]",
                  )}
                />
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2 type-caption-bold text-fg-primary">
                    v{v.version_number}
                    {isLatest && (
                      <Badge variant="info" className="capitalize">
                        latest
                      </Badge>
                    )}
                    {v.unpublished_at && (
                      <Badge variant="warning">retracted</Badge>
                    )}
                    <span className="type-caption text-fg-disabled ml-auto">
                      {new Date(v.published_at).toLocaleDateString()}
                    </span>
                  </div>
                  {v.release_notes && (
                    <p className="mt-0.5 line-clamp-2 type-caption text-fg-muted">
                      {v.release_notes}
                    </p>
                  )}
                </div>
                <div className="relative">
                  <button
                    type="button"
                    onClick={() => setOpenId(menuOpen ? null : v.id)}
                    disabled={busy}
                    aria-label={`Actions for v${v.version_number}`}
                    className="rounded-sm p-1 text-fg-muted hover:bg-white/[0.05] hover:text-fg-primary transition-colors"
                  >
                    <MoreHorizontal size={14} strokeWidth={2} />
                  </button>
                  {menuOpen && (
                    <div
                      role="menu"
                      onMouseLeave={() => setOpenId(null)}
                      className="absolute right-0 top-full z-10 mt-1 w-52 overflow-hidden rounded-md bg-popover border border-white/[0.08] shadow-5 py-1 animate-fade-in-fast"
                    >
                      {!isLatest && !v.unpublished_at && (
                        <button
                          type="button"
                          onClick={() =>
                            handle(() =>
                              catalogApi.rollback(catalogAgentId, v.id),
                            )
                          }
                          className="block w-full text-left px-3 py-2 type-caption text-fg-secondary hover:bg-white/[0.05]"
                        >
                          Set v{v.version_number} as latest
                        </button>
                      )}
                      {!v.unpublished_at && (
                        <button
                          type="button"
                          onClick={() =>
                            handle(
                              () => catalogApi.unpublishVersion(v.id),
                              `Retract v${v.version_number}? Users pinned to it keep access.`,
                            )
                          }
                          className="block w-full text-left px-3 py-2 type-caption text-fg-secondary hover:bg-white/[0.05]"
                        >
                          Unpublish this version
                        </button>
                      )}
                      {isLatest && (
                        <button
                          type="button"
                          disabled
                          className="block w-full text-left px-3 py-2 type-caption text-fg-disabled cursor-not-allowed"
                        >
                          Current latest
                        </button>
                      )}
                    </div>
                  )}
                </div>
              </li>
            );
          })}
          {versions.length > 5 && !showAll && (
            <li className="pt-2">
              <Button
                variant="ghost"
                size="sm"
                onClick={() => setShowAll(true)}
              >
                Show {versions.length - 5} older
              </Button>
            </li>
          )}
        </ol>
      )}

      {error && (
        <p className="mt-3 type-caption text-danger" role="alert">
          {error}
        </p>
      )}
    </section>
  );
}
