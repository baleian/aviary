"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useState } from "react";
import { ArrowRight, Check, Download, Flame, Sparkles } from "@/components/icons";
import { Badge } from "@/components/ui/badge";
import { Spinner } from "@/components/ui/spinner";
import { routes } from "@/lib/constants/routes";
import { cn } from "@/lib/utils";
import type { CatalogAgentSummary } from "@/types/catalog";

interface CatalogFeaturedProps {
  items: CatalogAgentSummary[];
  importedMap: Record<string, string>;
  onImport: (item: CatalogAgentSummary) => Promise<void>;
}

export function CatalogFeatured({ items, importedMap, onImport }: CatalogFeaturedProps) {
  if (items.length === 0) return null;
  const [primary, secondary] = items;

  return (
    <section>
      <SectionHeader
        icon={<Flame size={14} strokeWidth={1.75} className="text-aurora-coral" />}
        label="Featured"
      />
      <div className="mt-4 grid gap-4 lg:grid-cols-2">
        {primary && (
          <FeaturedCard
            item={primary}
            importedAgentId={importedMap[primary.id]}
            onImport={() => onImport(primary)}
            accent="coral"
          />
        )}
        {secondary && (
          <FeaturedCard
            item={secondary}
            importedAgentId={importedMap[secondary.id]}
            onImport={() => onImport(secondary)}
            accent="violet"
          />
        )}
      </div>
    </section>
  );
}

function FeaturedCard({
  item,
  importedAgentId,
  onImport,
  accent,
}: {
  item: CatalogAgentSummary;
  importedAgentId?: string;
  onImport: () => Promise<void>;
  accent: "coral" | "violet";
}) {
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

  const accentGlow =
    accent === "coral"
      ? "bg-aurora-c"
      : "bg-aurora-a";
  const hoverGlow =
    accent === "coral"
      ? "hover:shadow-[0_12px_40px_rgba(0,0,0,0.45),0_0_0_1px_rgba(255,122,89,0.30),0_0_60px_rgba(255,122,89,0.18)]"
      : "hover:shadow-[0_12px_40px_rgba(0,0,0,0.45),0_0_0_1px_rgba(123,92,255,0.30),0_0_60px_rgba(123,92,255,0.18)]";

  return (
    <Link href={routes.catalogAgent(item.id)} className="group block">
      <article
        className={cn(
          "relative isolate flex h-[220px] flex-col overflow-hidden rounded-xl p-6 glass-pane",
          "transition-all duration-[320ms] ease-[cubic-bezier(0.16,1,0.3,1)]",
          "hover:-translate-y-[2px] hover:bg-white/[0.07]",
          hoverGlow,
        )}
      >
        <span
          aria-hidden
          className={cn(
            "pointer-events-none absolute -right-16 -top-16 -z-10 h-60 w-60 rounded-full opacity-[0.18] blur-3xl",
            accentGlow,
          )}
        />
        <span
          aria-hidden
          className="pointer-events-none absolute left-0 top-0 -z-10 h-px w-full bg-gradient-to-r from-transparent via-white/20 to-transparent"
        />

        <div className="flex items-start gap-4">
          <div
            className={cn(
              "flex h-14 w-14 shrink-0 items-center justify-center rounded-lg border border-white/[0.10] text-2xl",
              accent === "coral" ? "bg-aurora-c-soft" : "bg-aurora-a-soft",
            )}
          >
            {item.icon || "🤖"}
          </div>
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2">
              <h3 className="truncate type-card-heading text-fg-primary group-hover:text-aurora-coral transition-colors">
                {item.name}
              </h3>
              <Badge
                variant="neutral"
                className="shrink-0 font-mono type-caption"
              >
                v{item.current_version_number}
              </Badge>
            </div>
            <p className="mt-1 truncate type-caption text-fg-muted">
              By {item.owner_email}
            </p>
            <p className="mt-3 line-clamp-2 type-body-tight text-fg-secondary">
              {item.description || "No description provided"}
            </p>
          </div>
        </div>

        <div className="mt-auto flex items-center justify-between gap-3">
          <div className="flex min-w-0 flex-wrap items-center gap-1.5">
            <Badge variant="neutral" className="capitalize">
              {item.category}
            </Badge>
            {item.import_count > 0 && (
              <span className="inline-flex items-center gap-1 type-caption text-fg-muted">
                <Sparkles size={10} strokeWidth={2} className="text-aurora-coral" />
                {item.import_count} imports
              </span>
            )}
          </div>
          {imported && importedAgentId ? (
            <button
              type="button"
              onClick={handleOpen}
              aria-label={`Open imported ${item.name}`}
              className={cn(
                "inline-flex items-center gap-1.5 rounded-pill px-4 py-1.5 type-button transition-colors",
                "bg-aurora-cyan/15 border border-aurora-cyan/40 text-aurora-cyan",
                "hover:bg-aurora-cyan/20",
                "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-aurora-cyan/40",
              )}
            >
              <Check size={14} strokeWidth={2} />
              Open
              <ArrowRight size={14} strokeWidth={2} />
            </button>
          ) : (
            <button
              type="button"
              onClick={handleImport}
              disabled={busy}
              aria-label={`Import ${item.name}`}
              className={cn(
                "inline-flex items-center gap-1.5 rounded-pill px-5 py-1.5 type-button transition-all duration-[320ms] ease-[cubic-bezier(0.16,1,0.3,1)]",
                "bg-aurora-a text-white animate-aurora-sheen",
                "shadow-[0_0_24px_rgba(123,92,255,0.38),inset_0_1px_0_rgba(255,255,255,0.18)]",
                "hover:-translate-y-[1px] hover:shadow-[0_0_32px_rgba(123,92,255,0.54),inset_0_1px_0_rgba(255,255,255,0.22)]",
                "disabled:opacity-60 disabled:cursor-not-allowed disabled:translate-y-0",
                "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-aurora-violet/60 focus-visible:ring-offset-2 focus-visible:ring-offset-canvas",
              )}
            >
              {busy ? (
                <>
                  <Spinner size={12} className="text-white" />
                  Importing…
                </>
              ) : (
                <>
                  <Download size={14} strokeWidth={2.25} />
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

export function SectionHeader({
  icon,
  label,
  actions,
}: {
  icon?: React.ReactNode;
  label: string;
  actions?: React.ReactNode;
}) {
  return (
    <div className="flex items-end justify-between gap-4">
      <div className="flex items-center gap-2">
        {icon}
        <h2 className="type-caption-bold uppercase tracking-wide text-fg-secondary">
          {label}
        </h2>
      </div>
      {actions && <div className="flex items-center gap-2">{actions}</div>}
    </div>
  );
}
