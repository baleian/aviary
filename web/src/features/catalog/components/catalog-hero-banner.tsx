"use client";

import { Search, Sparkles, Store } from "@/components/icons";
import { Input } from "@/components/ui/input";
import Link from "next/link";
import { routes } from "@/lib/constants/routes";

interface CatalogHeroBannerProps {
  total: number | null;
  categoriesCount: number | null;
  publishersCount: number | null;
  searchInput: string;
  onSearchChange: (value: string) => void;
  onCompositionStart: () => void;
  onCompositionEnd: (value: string) => void;
}

export function CatalogHeroBanner({
  total,
  categoriesCount,
  publishersCount,
  searchInput,
  onSearchChange,
  onCompositionStart,
  onCompositionEnd,
}: CatalogHeroBannerProps) {
  return (
    <section className="relative isolate overflow-hidden border-b border-white/[0.06]">
      <div
        aria-hidden
        className="pointer-events-none absolute inset-0 -z-10 bg-aurora-c opacity-[0.14]"
      />
      <div
        aria-hidden
        className="pointer-events-none absolute -left-40 top-1/2 -z-10 h-[520px] w-[520px] -translate-y-1/2 rounded-full bg-aurora-c opacity-[0.22] blur-3xl"
      />
      <div
        aria-hidden
        className="pointer-events-none absolute -right-32 top-0 -z-10 h-[440px] w-[440px] rounded-full bg-aurora-a opacity-[0.18] blur-3xl"
      />
      <div
        aria-hidden
        className="pointer-events-none absolute inset-x-0 bottom-0 -z-10 h-24 bg-gradient-to-b from-transparent to-canvas"
      />

      <div className="relative mx-auto max-w-[1600px] px-6 pb-14 pt-10 md:px-10 md:pb-16 md:pt-12">
        <div className="flex items-start justify-between gap-4">
          <div className="inline-flex items-center gap-2 rounded-pill glass-raised px-3 py-1 type-caption text-fg-muted">
            <Sparkles size={12} strokeWidth={1.75} className="text-aurora-coral" />
            <span>Discover</span>
          </div>
          <Link
            href={routes.catalogMine}
            className="inline-flex items-center gap-1.5 rounded-pill glass-raised px-3 py-1 type-caption text-fg-muted transition-colors hover:text-fg-primary"
          >
            <Store size={12} strokeWidth={1.75} className="text-aurora-coral" />
            My entries
          </Link>
        </div>

        <div className="mt-6 grid items-end gap-8 lg:grid-cols-[1fr_auto]">
          <div className="min-w-0">
            <h1 className="max-w-3xl text-balance type-display-hero tracking-tight">
              The <span className="text-aurora-c">Agent</span> catalog.
            </h1>
            <p className="mt-4 max-w-xl text-balance type-body-lg text-fg-muted">
              Discover, pin, and import agents published by your team. Every import
              keeps its conversations — fork any time to take ownership.
            </p>
          </div>

          {(total !== null ||
            categoriesCount !== null ||
            publishersCount !== null) && (
            <dl className="grid shrink-0 grid-cols-3 gap-x-8 text-left lg:text-right">
              <Stat label="Agents" value={total} tone="coral" />
              <Stat label="Categories" value={categoriesCount} tone="gold" />
              <Stat label="Publishers" value={publishersCount} tone="violet" />
            </dl>
          )}
        </div>

        <div className="mt-8 max-w-2xl">
          <div className="relative">
            <Search
              size={16}
              strokeWidth={1.75}
              className="pointer-events-none absolute left-4 top-1/2 -translate-y-1/2 text-fg-disabled"
            />
            <Input
              placeholder="Search agents by name, description, or author…"
              value={searchInput}
              onChange={(e) => onSearchChange(e.target.value)}
              onCompositionStart={onCompositionStart}
              onCompositionEnd={(e) =>
                onCompositionEnd((e.target as HTMLInputElement).value)
              }
              aria-label="Search catalog"
              className="h-12 rounded-pill border-white/[0.10] bg-white/[0.05] pl-11 pr-4 type-body shadow-2 backdrop-blur-sm transition-colors focus-visible:border-aurora-coral/50 focus-visible:shadow-[0_0_0_4px_rgba(255,122,89,0.14)]"
            />
          </div>
        </div>
      </div>
    </section>
  );
}

function Stat({
  label,
  value,
  tone,
}: {
  label: string;
  value: number | null;
  tone: "coral" | "gold" | "violet";
}) {
  const toneClass =
    tone === "coral"
      ? "text-aurora-coral"
      : tone === "gold"
        ? "text-aurora-amber"
        : "text-aurora-violet";
  return (
    <div className="flex flex-col">
      <dt className="type-caption uppercase tracking-wide text-fg-disabled">
        {label}
      </dt>
      <dd
        className={`mt-1 tabular-nums type-h1 font-semibold ${toneClass}`}
      >
        {value === null ? "—" : value}
      </dd>
    </div>
  );
}
