"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useState } from "react";
import { Badge } from "@/components/ui/badge";
import { ArrowRight, Download } from "@/components/icons";
import { Spinner } from "@/components/ui/spinner";
import { routes } from "@/lib/constants/routes";
import { cn } from "@/lib/utils";
import type { CatalogAgentSummary } from "@/types/catalog";

interface CatalogCardProps {
  item: CatalogAgentSummary;
  importedAgentId?: string | null;
  onImport: () => Promise<void>;
}

export function CatalogCard({ item, importedAgentId, onImport }: CatalogCardProps) {
  const router = useRouter();
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

  const handleOpenImported = (e: React.MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
    if (importedAgentId) router.push(routes.agent(importedAgentId));
  };

  return (
    <Link href={routes.catalogAgent(item.id)} className="group block">
      <article
        className={cn(
          "relative flex h-[284px] flex-col rounded-lg p-5 glass-pane shadow-2",
          "transition-all duration-[320ms] ease-[cubic-bezier(0.16,1,0.3,1)]",
          "hover:-translate-y-[1px] hover:bg-white/[0.07]",
          "hover:shadow-[0_8px_32px_rgba(0,0,0,0.45),0_0_0_1px_rgba(123,92,255,0.3),inset_0_1px_0_rgba(255,255,255,0.08),0_0_40px_rgba(123,92,255,0.18)]",
        )}
      >
        <div className="mb-4 flex items-start gap-3">
          <div className="flex h-12 w-12 shrink-0 items-center justify-center rounded-md bg-aurora-a-soft border border-white/[0.08] text-xl">
            {item.icon || "🤖"}
          </div>
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2">
              <h3 className="truncate type-card-heading text-fg-primary group-hover:text-aurora-violet transition-colors">
                {item.name}
              </h3>
              <Badge variant="neutral" className="shrink-0 font-mono type-caption">
                v{item.current_version_number}
              </Badge>
            </div>
            <p className="mt-0.5 truncate type-caption text-fg-muted">
              By {item.owner_email}
            </p>
          </div>
        </div>

        <p className="mb-4 line-clamp-3 flex-1 type-body-tight text-fg-muted">
          {item.description || "No description provided"}
        </p>

        <div className="mb-4 flex flex-wrap items-center gap-1.5">
          <Badge variant="neutral" className="capitalize">
            {item.category}
          </Badge>
          {item.mcp_servers.slice(0, 2).map((s) => (
            <Badge key={s} variant="neutral" className="font-mono type-caption">
              {s}
            </Badge>
          ))}
          {item.mcp_servers.length > 2 && (
            <span className="type-caption text-fg-disabled">
              +{item.mcp_servers.length - 2}
            </span>
          )}
        </div>

        <div className="flex items-center justify-between gap-2 border-t border-white/[0.06] pt-3">
          <span className="type-caption tabular-nums text-fg-disabled">
            {item.import_count} {item.import_count === 1 ? "import" : "imports"}
          </span>
          {imported && importedAgentId ? (
            <button
              type="button"
              onClick={handleOpenImported}
              aria-label={`Open imported ${item.name}`}
              className={cn(
                "inline-flex items-center gap-1.5 rounded-sm px-2.5 py-1 type-caption-bold transition-colors",
                "bg-aurora-cyan/15 border border-aurora-cyan/40 text-aurora-cyan",
                "hover:bg-aurora-cyan/20",
                "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-aurora-cyan/40",
              )}
            >
              Open
              <ArrowRight size={12} strokeWidth={2.5} />
            </button>
          ) : (
            <button
              type="button"
              onClick={handleImport}
              disabled={busy}
              aria-label={`Import ${item.name}`}
              className={cn(
                "inline-flex items-center gap-1.5 rounded-pill px-4 py-1 type-caption-bold transition-all duration-[320ms] ease-[cubic-bezier(0.16,1,0.3,1)]",
                "bg-aurora-a text-white animate-aurora-sheen",
                "shadow-[0_0_20px_rgba(123,92,255,0.35),inset_0_1px_0_rgba(255,255,255,0.18)]",
                "hover:-translate-y-[1px] hover:shadow-[0_0_28px_rgba(123,92,255,0.5),inset_0_1px_0_rgba(255,255,255,0.22)]",
                "disabled:opacity-60 disabled:cursor-not-allowed disabled:translate-y-0",
                "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-aurora-violet/60 focus-visible:ring-offset-2 focus-visible:ring-offset-canvas",
              )}
            >
              {busy ? (
                <>
                  <Spinner size={11} className="text-white" />
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
