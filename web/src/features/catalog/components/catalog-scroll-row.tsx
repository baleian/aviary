"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { ChevronLeft, ChevronRight } from "@/components/icons";
import { cn } from "@/lib/utils";
import type { CatalogAgentSummary } from "@/types/catalog";
import { CatalogCompactCard } from "./catalog-compact-card";
import { SectionHeader } from "./catalog-featured";

interface CatalogScrollRowProps {
  icon: React.ReactNode;
  label: string;
  action?: React.ReactNode;
  items: CatalogAgentSummary[];
  importedMap: Record<string, string>;
  onImport: (item: CatalogAgentSummary) => Promise<void>;
}

export function CatalogScrollRow({
  icon,
  label,
  action,
  items,
  importedMap,
  onImport,
}: CatalogScrollRowProps) {
  const scrollRef = useRef<HTMLDivElement>(null);
  const [canLeft, setCanLeft] = useState(false);
  const [canRight, setCanRight] = useState(false);

  const updateScrollState = useCallback(() => {
    const el = scrollRef.current;
    if (!el) return;
    setCanLeft(el.scrollLeft > 4);
    setCanRight(el.scrollLeft + el.clientWidth < el.scrollWidth - 4);
  }, []);

  useEffect(() => {
    updateScrollState();
  }, [items, updateScrollState]);

  const nudge = (dir: -1 | 1) => {
    const el = scrollRef.current;
    if (!el) return;
    const delta = Math.round(el.clientWidth * 0.8) * dir;
    el.scrollBy({ left: delta, behavior: "smooth" });
  };

  if (items.length === 0) return null;

  return (
    <section>
      <SectionHeader
        icon={icon}
        label={label}
        actions={
          <div className="flex items-center gap-1.5">
            {action}
            <button
              type="button"
              onClick={() => nudge(-1)}
              disabled={!canLeft}
              aria-label={`Scroll ${label} left`}
              className={cn(
                "flex h-7 w-7 items-center justify-center rounded-full border border-white/[0.08] bg-white/[0.04] text-fg-secondary transition-all",
                "hover:bg-white/[0.08] hover:text-fg-primary",
                "disabled:opacity-30 disabled:cursor-not-allowed",
                "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-aurora-violet/40",
              )}
            >
              <ChevronLeft size={14} strokeWidth={2} />
            </button>
            <button
              type="button"
              onClick={() => nudge(1)}
              disabled={!canRight}
              aria-label={`Scroll ${label} right`}
              className={cn(
                "flex h-7 w-7 items-center justify-center rounded-full border border-white/[0.08] bg-white/[0.04] text-fg-secondary transition-all",
                "hover:bg-white/[0.08] hover:text-fg-primary",
                "disabled:opacity-30 disabled:cursor-not-allowed",
                "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-aurora-violet/40",
              )}
            >
              <ChevronRight size={14} strokeWidth={2} />
            </button>
          </div>
        }
      />
      <div className="relative mt-4">
        <div
          ref={scrollRef}
          onScroll={updateScrollState}
          className="scrollbar-hidden -mx-6 flex snap-x snap-mandatory gap-3 overflow-x-auto px-6 pb-2 md:-mx-10 md:px-10"
        >
          {items.map((item) => (
            <div key={item.id} className="snap-start">
              <CatalogCompactCard
                item={item}
                importedAgentId={importedMap[item.id] ?? null}
                onImport={() => onImport(item)}
              />
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}
