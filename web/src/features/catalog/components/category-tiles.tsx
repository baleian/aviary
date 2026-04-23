"use client";

import {
  BookOpen,
  Code2,
  Database,
  Layers,
  Megaphone,
  Pencil,
  Settings,
  Zap,
} from "@/components/icons";
import type { CategoryFacet } from "@/types/catalog";
import { cn } from "@/lib/utils";

interface CategoryTilesProps {
  facets: CategoryFacet[];
  selected: string | null;
  onSelect: (category: string | null) => void;
}

const CATEGORY_META: Record<
  string,
  { label: string; icon: typeof Code2; blurb: string }
> = {
  coding: { label: "Coding", icon: Code2, blurb: "Dev copilots & code review" },
  research: { label: "Research", icon: BookOpen, blurb: "Deep reads & synthesis" },
  writing: { label: "Writing", icon: Pencil, blurb: "Drafting & editing" },
  productivity: { label: "Productivity", icon: Zap, blurb: "Inbox, docs, calendar" },
  marketing: { label: "Marketing", icon: Megaphone, blurb: "Campaigns & copy" },
  data: { label: "Data", icon: Database, blurb: "Analysis & queries" },
  ops: { label: "Ops", icon: Settings, blurb: "Infra & incident" },
  other: { label: "Other", icon: Layers, blurb: "Everything else" },
};

const CATEGORY_ORDER = [
  "coding",
  "research",
  "writing",
  "productivity",
  "marketing",
  "data",
  "ops",
  "other",
];

export function CategoryTiles({ facets, selected, onSelect }: CategoryTilesProps) {
  const countMap = new Map(facets.map((f) => [f.name, f.count]));

  return (
    <div className="-mx-6 overflow-x-auto px-6 md:-mx-10 md:px-10">
      <div className="grid min-w-max grid-cols-8 gap-3 pb-1 md:min-w-0">
        {CATEGORY_ORDER.map((key) => {
          const meta = CATEGORY_META[key];
          const count = countMap.get(key) ?? 0;
          const active = selected === key;
          const Icon = meta.icon;
          return (
            <button
              key={key}
              type="button"
              onClick={() => onSelect(active ? null : key)}
              aria-pressed={active}
              className={cn(
                "group relative flex h-[120px] w-full flex-col items-start justify-between overflow-hidden rounded-lg border p-4 text-left",
                "transition-all duration-[320ms] ease-[cubic-bezier(0.16,1,0.3,1)]",
                "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-aurora-coral/40",
                active
                  ? "border-transparent bg-aurora-c text-fg-on-light shadow-[0_8px_28px_rgba(255,122,89,0.32),inset_0_1px_0_rgba(255,255,255,0.18)]"
                  : "border-white/[0.08] bg-white/[0.03] text-fg-secondary hover:-translate-y-[1px] hover:border-aurora-coral/30 hover:bg-white/[0.06] hover:shadow-[0_8px_24px_rgba(0,0,0,0.35),0_0_0_1px_rgba(255,122,89,0.18),0_0_28px_rgba(255,122,89,0.10)]",
              )}
            >
              {!active && (
                <span
                  aria-hidden
                  className="pointer-events-none absolute -right-6 -top-6 h-20 w-20 rounded-full bg-aurora-c opacity-[0.10] blur-2xl transition-opacity duration-[320ms] group-hover:opacity-[0.18]"
                />
              )}
              <Icon
                size={20}
                strokeWidth={1.75}
                className={cn(
                  "transition-colors",
                  active ? "text-fg-on-light" : "text-aurora-coral",
                )}
              />
              <div>
                <div
                  className={cn(
                    "type-card-heading",
                    active ? "text-fg-on-light" : "text-fg-primary",
                  )}
                >
                  {meta.label}
                </div>
                <div
                  className={cn(
                    "mt-0.5 type-caption tabular-nums",
                    active ? "text-fg-on-light/80" : "text-fg-disabled",
                  )}
                >
                  {count} {count === 1 ? "agent" : "agents"}
                </div>
              </div>
            </button>
          );
        })}
      </div>
    </div>
  );
}
