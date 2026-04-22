"use client";

import { useState } from "react";
import Link from "next/link";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { MoreHorizontal } from "@/components/icons";
import { catalogApi } from "@/features/catalog/api/catalog-api";
import { routes } from "@/lib/constants/routes";
import { cn } from "@/lib/utils";
import type { MyCatalogAgent } from "@/types/catalog";

interface MyCatalogCardProps {
  item: MyCatalogAgent;
  onMutated: () => void;
}

export function MyCatalogCard({ item, onMutated }: MyCatalogCardProps) {
  const [menuOpen, setMenuOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const handle = async (fn: () => Promise<unknown>, confirmText?: string) => {
    if (confirmText && !confirm(confirmText)) return;
    setBusy(true);
    setErr(null);
    try {
      await fn();
      onMutated();
    } catch (e) {
      setErr(e instanceof Error ? e.message : "Failed");
    } finally {
      setBusy(false);
      setMenuOpen(false);
    }
  };

  return (
    <article
      className={cn(
        "relative flex h-full flex-col rounded-lg p-5 glass-pane shadow-2",
        item.is_published
          ? ""
          : "opacity-75",
      )}
    >
      {/* Header */}
      <div className="mb-3 flex items-start justify-between gap-2">
        <div className="flex min-w-0 items-center gap-3">
          <div className="flex h-11 w-11 shrink-0 items-center justify-center rounded-md bg-aurora-c-soft border border-white/[0.08] text-lg">
            {item.icon || "🤖"}
          </div>
          <div className="min-w-0">
            <Link
              href={routes.catalogAgent(item.id)}
              className="type-body truncate text-fg-primary hover:text-aurora-coral transition-colors"
            >
              {item.name}
            </Link>
            <div className="flex items-center gap-1.5 type-caption text-fg-muted">
              {item.current_version_number !== null ? (
                <>v{item.current_version_number}</>
              ) : (
                <>No versions</>
              )}
              <span className="text-fg-disabled">·</span>
              <span className="capitalize">{item.category}</span>
            </div>
          </div>
        </div>

        {item.is_published ? (
          <Badge
            variant="success"
            className="shrink-0"
          >
            Live
          </Badge>
        ) : (
          <Badge variant="warning" className="shrink-0">
            Unpublished
          </Badge>
        )}
      </div>

      {/* Body */}
      <p className="mb-4 line-clamp-2 flex-1 type-body-tight text-fg-muted">
        {item.description || "No description provided"}
      </p>

      {/* Footer */}
      <div className="flex items-center justify-between gap-2 border-t border-white/[0.06] pt-3">
        <div className="type-caption text-fg-disabled">
          {item.import_count === 0
            ? "No imports yet"
            : `${item.import_count} import${item.import_count === 1 ? "" : "s"}`}
        </div>

        <div className="relative">
          <Button
            variant="ghost"
            size="sm"
            onClick={() => setMenuOpen((v) => !v)}
            disabled={busy}
            aria-label="Catalog actions"
          >
            <MoreHorizontal size={14} strokeWidth={2} />
          </Button>
          {menuOpen && (
            <div
              role="menu"
              className="absolute right-0 top-full z-10 mt-1 w-56 overflow-hidden rounded-md bg-popover border border-white/[0.08] shadow-5 py-1 animate-fade-in-fast"
              onMouseLeave={() => setMenuOpen(false)}
            >
              {item.linked_agent_id ? (
                <Link
                  href={routes.agent(item.linked_agent_id)}
                  className="block px-3 py-2 type-caption text-fg-secondary hover:bg-white/[0.05]"
                  onClick={() => setMenuOpen(false)}
                >
                  Open linked working copy →
                </Link>
              ) : (
                <button
                  type="button"
                  onClick={() =>
                    handle(
                      () => catalogApi.restore(item.id),
                      "Create a new editable working copy from the current catalog snapshot?",
                    )
                  }
                  className="block w-full text-left px-3 py-2 type-caption text-fg-secondary hover:bg-white/[0.05]"
                >
                  Open as working copy
                </button>
              )}
              <MenuDivider />
              {item.is_published ? (
                <button
                  type="button"
                  onClick={() =>
                    handle(
                      () => catalogApi.unpublishCatalog(item.id),
                      "Hide this agent from the catalog? Existing imports keep working.",
                    )
                  }
                  className="block w-full text-left px-3 py-2 type-caption text-fg-secondary hover:bg-white/[0.05]"
                >
                  Unpublish
                </button>
              ) : (
                <button
                  type="button"
                  onClick={() => handle(() => catalogApi.republishCatalog(item.id))}
                  className="block w-full text-left px-3 py-2 type-caption text-fg-secondary hover:bg-white/[0.05]"
                >
                  Republish
                </button>
              )}
              <MenuDivider />
              <button
                type="button"
                onClick={() =>
                  handle(
                    () => catalogApi.deleteCatalogAgent(item.id),
                    "Permanently delete this catalog entry? This can't be undone.",
                  )
                }
                className="block w-full text-left px-3 py-2 type-caption text-danger hover:bg-danger/10"
              >
                Delete catalog entry
              </button>
            </div>
          )}
        </div>
      </div>

      {err && (
        <p className="mt-2 type-caption text-danger" role="alert">
          {err}
        </p>
      )}
    </article>
  );
}

function MenuDivider() {
  return <div className="my-1 h-px bg-white/[0.06]" />;
}
