"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useState } from "react";
import { ArrowRight, Check, Download } from "@/components/icons";
import { Badge } from "@/components/ui/badge";
import { Spinner } from "@/components/ui/spinner";
import { routes } from "@/lib/constants/routes";
import { cn } from "@/lib/utils";
import type { CatalogAgentSummary } from "@/types/catalog";

interface CatalogCompactCardProps {
  item: CatalogAgentSummary;
  importedAgentId?: string | null;
  onImport: () => Promise<void>;
}

export function CatalogCompactCard({
  item,
  importedAgentId,
  onImport,
}: CatalogCompactCardProps) {
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

  const handleOpen = (e: React.MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
    if (importedAgentId) router.push(routes.agent(importedAgentId));
  };

  return (
    <Link
      href={routes.catalogAgent(item.id)}
      className="group block w-[280px] shrink-0"
    >
      <article
        className={cn(
          "relative flex h-[196px] flex-col rounded-lg p-4 glass-pane",
          "transition-all duration-[320ms] ease-[cubic-bezier(0.16,1,0.3,1)]",
          "hover:-translate-y-[1px] hover:bg-white/[0.07]",
          "hover:shadow-[0_8px_32px_rgba(0,0,0,0.45),0_0_0_1px_rgba(123,92,255,0.3),0_0_40px_rgba(123,92,255,0.18)]",
        )}
      >
        <div className="flex items-center gap-3">
          <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-md bg-aurora-a-soft border border-white/[0.08] text-lg">
            {item.icon || "🤖"}
          </div>
          <div className="min-w-0 flex-1">
            <h3 className="truncate type-body font-medium text-fg-primary group-hover:text-aurora-violet transition-colors">
              {item.name}
            </h3>
            <p className="truncate type-caption text-fg-muted">
              v{item.current_version_number} · {item.owner_email}
            </p>
          </div>
        </div>

        <p className="mt-3 line-clamp-2 flex-1 type-caption text-fg-muted">
          {item.description || "No description provided"}
        </p>

        <div className="mt-3 flex items-center justify-between gap-2 border-t border-white/[0.06] pt-3">
          <Badge variant="neutral" className="capitalize">
            {item.category}
          </Badge>
          {imported && importedAgentId ? (
            <button
              type="button"
              onClick={handleOpen}
              aria-label={`Open imported ${item.name}`}
              className={cn(
                "inline-flex items-center gap-1 rounded-sm px-2 py-1 type-caption-bold transition-colors",
                "bg-aurora-cyan/15 border border-aurora-cyan/40 text-aurora-cyan",
                "hover:bg-aurora-cyan/20",
              )}
            >
              Open
              <ArrowRight size={11} strokeWidth={2.5} />
            </button>
          ) : (
            <button
              type="button"
              onClick={handleImport}
              disabled={busy}
              aria-label={`Import ${item.name}`}
              className={cn(
                "inline-flex items-center gap-1 rounded-pill px-3 py-1 type-caption-bold transition-all",
                "bg-aurora-a text-white animate-aurora-sheen",
                "shadow-[0_0_16px_rgba(123,92,255,0.30),inset_0_1px_0_rgba(255,255,255,0.18)]",
                "hover:-translate-y-[1px] hover:shadow-[0_0_22px_rgba(123,92,255,0.44),inset_0_1px_0_rgba(255,255,255,0.22)]",
                "disabled:opacity-60 disabled:cursor-not-allowed",
              )}
            >
              {busy ? (
                <Spinner size={10} className="text-white" />
              ) : (
                <Download size={11} strokeWidth={2.5} />
              )}
              Import
            </button>
          )}
        </div>
      </article>
    </Link>
  );
}
