"use client";

import Link from "next/link";
import { useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Check, Download, Plus } from "@/components/icons";
import { Spinner } from "@/components/ui/spinner";
import { routes } from "@/lib/constants/routes";
import { cn } from "@/lib/utils";
import type { CatalogAgentSummary } from "@/types/catalog";

interface CatalogCardProps {
  item: CatalogAgentSummary;
  importedAgentId?: string | null;
  onImport: () => Promise<void>;
}

/**
 * CatalogCard — aurora-A glass tile (same hover glow as /agents cards so
 * hover is learned once across the app). Phase 1: immediate Import button
 * instead of cart; cart drawer comes in Phase 3.
 */
export function CatalogCard({ item, importedAgentId, onImport }: CatalogCardProps) {
  const [busy, setBusy] = useState(false);
  const imported = Boolean(importedAgentId);

  const handleImport = async (e: React.MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
    if (busy || imported) return;
    setBusy(true);
    try {
      await onImport();
    } finally {
      setBusy(false);
    }
  };

  return (
    <Link href={routes.catalogAgent(item.id)} className="group block">
      <article
        className={cn(
          "relative flex h-full flex-col rounded-lg p-5 glass-pane shadow-2",
          "transition-all duration-[320ms] ease-[cubic-bezier(0.16,1,0.3,1)]",
          "hover:-translate-y-[1px] hover:bg-white/[0.07]",
          "hover:shadow-[0_8px_32px_rgba(0,0,0,0.45),0_0_0_1px_rgba(123,92,255,0.3),inset_0_1px_0_rgba(255,255,255,0.08),0_0_40px_rgba(123,92,255,0.18)]",
        )}
      >
        {/* Header */}
        <div className="mb-3 flex items-start justify-between gap-2">
          <div className="flex min-w-0 items-center gap-3">
            <div className="flex h-11 w-11 shrink-0 items-center justify-center rounded-md bg-aurora-a-soft border border-white/[0.08] text-lg">
              {item.icon || "🤖"}
            </div>
            <div className="min-w-0">
              <h3 className="type-body truncate text-fg-primary group-hover:text-aurora-violet transition-colors">
                {item.name}
              </h3>
              <p className="type-caption text-fg-muted truncate">
                {item.owner_email}
              </p>
            </div>
          </div>
          <Badge variant="muted" className="shrink-0">v{item.current_version_number}</Badge>
        </div>

        {/* Description */}
        <p className="mb-3 line-clamp-2 flex-1 type-body-tight text-fg-muted">
          {item.description || "No description provided"}
        </p>

        {/* Category + MCP servers */}
        <div className="mb-4 flex flex-wrap items-center gap-1.5">
          <Badge variant="neutral" className="capitalize">{item.category}</Badge>
          {item.mcp_servers.slice(0, 3).map((srv) => (
            <Badge key={srv} variant="neutral" className="font-mono">{srv}</Badge>
          ))}
          {item.mcp_servers.length > 3 && (
            <Badge variant="neutral">+{item.mcp_servers.length - 3}</Badge>
          )}
        </div>

        {/* Footer */}
        <div className="flex items-center justify-end gap-2 border-t border-white/[0.06] pt-3">
          {imported ? (
            <span
              onClick={(e) => e.stopPropagation()}
              className="inline-flex items-center gap-1.5 rounded-sm px-2.5 py-1 type-caption-bold bg-aurora-cyan/15 border border-aurora-cyan/40 text-aurora-cyan"
            >
              <Check size={12} strokeWidth={2.5} /> Imported
            </span>
          ) : (
            <button
              type="button"
              onClick={handleImport}
              disabled={busy}
              aria-label={`Import ${item.name}`}
              className={cn(
                "inline-flex items-center gap-1.5 rounded-sm px-2.5 py-1 type-caption-bold transition-all duration-200",
                "focus-visible:outline-none",
                "bg-white/[0.05] border border-white/[0.08] text-fg-secondary",
                "hover:bg-white/[0.09] hover:border-white/[0.14] hover:text-fg-primary",
                "disabled:opacity-60 disabled:cursor-not-allowed",
              )}
            >
              {busy ? (
                <>
                  <Spinner size={11} className="text-fg-secondary" />
                  Importing…
                </>
              ) : (
                <>
                  <Download size={12} strokeWidth={2.5} />
                  Import
                </>
              )}
            </button>
          )}
        </div>
      </article>
    </Link>
  );
}
