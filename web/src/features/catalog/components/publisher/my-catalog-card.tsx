"use client";

import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { MoreHorizontal } from "@/components/icons";
import { catalogApi } from "@/features/catalog/api/catalog-api";
import { UnpublishDialog } from "@/features/catalog/components/publisher/unpublish-dialog";
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
  const [unpublishOpen, setUnpublishOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [restoreOpen, setRestoreOpen] = useState(false);
  const menuRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!menuOpen) return;
    const onDoc = (e: MouseEvent) => {
      if (!menuRef.current?.contains(e.target as Node)) setMenuOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setMenuOpen(false);
    };
    document.addEventListener("mousedown", onDoc);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDoc);
      document.removeEventListener("keydown", onKey);
    };
  }, [menuOpen]);

  const run = async (fn: () => Promise<unknown>) => {
    setBusy(true);
    setErr(null);
    try {
      await fn();
      onMutated();
    } catch (e) {
      setErr(e instanceof Error ? e.message : "Failed");
      throw e;
    } finally {
      setBusy(false);
      setMenuOpen(false);
    }
  };

  return (
    <article
      className={cn(
        "relative flex h-full flex-col rounded-lg p-5 glass-pane shadow-2",
        "transition-all duration-[320ms] ease-[cubic-bezier(0.16,1,0.3,1)]",
        "hover:-translate-y-[1px] hover:bg-white/[0.07]",
        "hover:shadow-[0_8px_32px_rgba(0,0,0,0.45),0_0_0_1px_rgba(123,92,255,0.3),inset_0_1px_0_rgba(255,255,255,0.08),0_0_40px_rgba(123,92,255,0.18)]",
        item.is_published ? "" : "opacity-75",
      )}
    >
      <div className="mb-3 flex items-start justify-between gap-2">
        <div className="flex min-w-0 items-center gap-3">
          <div className="flex h-11 w-11 shrink-0 items-center justify-center rounded-md bg-aurora-a-soft border border-white/[0.08] text-lg">
            {item.icon || "🤖"}
          </div>
          <div className="min-w-0">
            <Link
              href={routes.catalogAgent(item.id)}
              className="type-body truncate text-fg-primary hover:text-aurora-violet transition-colors"
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
          <Badge variant="success" className="shrink-0">Live</Badge>
        ) : (
          <Badge variant="warning" className="shrink-0">Unpublished</Badge>
        )}
      </div>

      <p className="mb-4 line-clamp-2 flex-1 type-body-tight text-fg-muted">
        {item.description || "No description provided"}
      </p>

      <div className="flex items-center justify-between gap-2 border-t border-white/[0.06] pt-3">
        <div className="type-caption text-fg-disabled">
          {item.import_count === 0
            ? "No imports yet"
            : `${item.import_count} import${item.import_count === 1 ? "" : "s"}`}
        </div>

        <div ref={menuRef} className="relative">
          <Button
            variant="ghost"
            size="sm"
            onClick={() => setMenuOpen((v) => !v)}
            disabled={busy}
            aria-haspopup="menu"
            aria-expanded={menuOpen}
            aria-label="Catalog actions"
          >
            <MoreHorizontal size={14} strokeWidth={2} />
          </Button>
          {menuOpen && (
            <div
              role="menu"
              className="absolute right-0 top-full z-10 mt-1 w-56 overflow-hidden rounded-md bg-popover border border-white/[0.08] shadow-5 py-1 animate-fade-in-fast"
            >
              {item.linked_agent_id ? (
                <Link
                  role="menuitem"
                  href={routes.agent(item.linked_agent_id)}
                  className="block px-3 py-2 type-caption text-fg-secondary hover:bg-white/[0.05]"
                  onClick={() => setMenuOpen(false)}
                >
                  Open linked working copy →
                </Link>
              ) : (
                <button
                  type="button"
                  role="menuitem"
                  onClick={() => {
                    setMenuOpen(false);
                    setRestoreOpen(true);
                  }}
                  className="block w-full text-left px-3 py-2 type-caption text-fg-secondary hover:bg-white/[0.05]"
                >
                  Open as working copy
                </button>
              )}
              <MenuDivider />
              {item.is_published ? (
                <button
                  type="button"
                  role="menuitem"
                  onClick={() => {
                    setMenuOpen(false);
                    setUnpublishOpen(true);
                  }}
                  className="block w-full text-left px-3 py-2 type-caption text-fg-secondary hover:bg-white/[0.05]"
                >
                  Unpublish
                </button>
              ) : (
                <button
                  type="button"
                  role="menuitem"
                  onClick={() => void run(() => catalogApi.republishCatalog(item.id))}
                  className="block w-full text-left px-3 py-2 type-caption text-fg-secondary hover:bg-white/[0.05]"
                >
                  Republish
                </button>
              )}
              <MenuDivider />
              <button
                type="button"
                role="menuitem"
                onClick={() => {
                  setMenuOpen(false);
                  setDeleteOpen(true);
                }}
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

      <UnpublishDialog
        open={unpublishOpen}
        onClose={() => setUnpublishOpen(false)}
        agentName={item.name}
        importCount={item.import_count}
        onConfirm={() => run(() => catalogApi.unpublishCatalog(item.id))}
      />

      <Dialog
        open={deleteOpen}
        onClose={() => setDeleteOpen(false)}
        title={`Delete ${item.name}?`}
        description="This permanently removes the catalog entry. Active imports will block deletion."
        footer={
          <>
            <Button variant="ghost" onClick={() => setDeleteOpen(false)} disabled={busy}>
              Cancel
            </Button>
            <Button
              variant="danger"
              disabled={busy}
              onClick={async () => {
                try {
                  await run(() => catalogApi.deleteCatalogAgent(item.id));
                  setDeleteOpen(false);
                } catch {
                  /* stays open on error so user can read the message */
                }
              }}
            >
              {busy ? "Deleting…" : "Delete"}
            </Button>
          </>
        }
      >
        <p className="type-body text-fg-secondary">
          Deleting discards the catalog entry and every version snapshot. If
          any user has imported this agent, the delete will fail — unpublish
          first to stop new imports, or wait until imports are gone.
        </p>
      </Dialog>

      <Dialog
        open={restoreOpen}
        onClose={() => setRestoreOpen(false)}
        title={`Open ${item.name} as working copy`}
        description="Create a fresh editable working copy linked to this catalog entry."
        footer={
          <>
            <Button variant="ghost" onClick={() => setRestoreOpen(false)} disabled={busy}>
              Cancel
            </Button>
            <Button
              variant="cta"
              disabled={busy}
              onClick={async () => {
                try {
                  await run(() => catalogApi.restore(item.id));
                  setRestoreOpen(false);
                } catch {
                  /* stays open on error */
                }
              }}
            >
              {busy ? "Opening…" : "Open working copy"}
            </Button>
          </>
        }
      >
        <p className="type-body text-fg-secondary">
          We'll snapshot the current catalog version into a new private agent
          owned by you. Visibility stays the same — unpublished entries remain
          hidden until you publish the new working copy.
        </p>
      </Dialog>
    </article>
  );
}

function MenuDivider() {
  return <div className="my-1 h-px bg-white/[0.06]" />;
}
