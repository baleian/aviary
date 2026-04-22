"use client";

import { Check, Filter, Layers } from "@/components/icons";
import { cn } from "@/lib/utils";
import type { CatalogFacets } from "@/types/catalog";

interface CatalogFilterRailProps {
  facets: CatalogFacets | null;
  selectedCategory: string | null;
  selectedServers: string[];
  onCategory: (c: string | null) => void;
  onToggleServer: (server: string) => void;
  onReset: () => void;
}

/**
 * Left rail — single-select categories + multi-select MCP servers.
 * Modeled after ToolSelector's rail for familiarity.
 */
export function CatalogFilterRail({
  facets,
  selectedCategory,
  selectedServers,
  onCategory,
  onToggleServer,
  onReset,
}: CatalogFilterRailProps) {
  const hasActiveFilters =
    selectedCategory !== null || selectedServers.length > 0;

  return (
    <aside className="flex flex-col gap-4 rounded-lg glass-pane p-4">
      {/* All agents */}
      <RailButton
        active={selectedCategory === null && selectedServers.length === 0}
        onClick={onReset}
        icon={<Layers size={14} strokeWidth={1.75} />}
        label="All agents"
      />

      {/* Categories */}
      <div className="flex flex-col gap-1">
        <div className="px-2 pb-1 type-caption text-fg-disabled tracking-wide uppercase">
          Categories
        </div>
        {(facets?.categories ?? []).map((c) => (
          <RailButton
            key={c.name}
            active={selectedCategory === c.name}
            onClick={() =>
              onCategory(selectedCategory === c.name ? null : c.name)
            }
            label={<span className="capitalize">{c.name}</span>}
            count={c.count}
          />
        ))}
        {facets?.categories?.length === 0 && (
          <div className="px-2 type-caption text-fg-disabled">No categories yet</div>
        )}
      </div>

      {/* MCP Servers */}
      {facets && facets.servers.length > 0 && (
        <div className="flex flex-col gap-1">
          <div className="px-2 pb-1 type-caption text-fg-disabled tracking-wide uppercase">
            MCP Servers
          </div>
          {facets.servers.map((s) => {
            const active = selectedServers.includes(s.name);
            return (
              <RailCheckbox
                key={s.name}
                active={active}
                onClick={() => onToggleServer(s.name)}
                label={s.name}
                count={s.count}
              />
            );
          })}
        </div>
      )}

      {hasActiveFilters && (
        <button
          type="button"
          onClick={onReset}
          className="mt-1 flex items-center gap-1.5 rounded-sm px-2 py-1 type-caption text-fg-muted hover:text-fg-primary hover:bg-white/[0.04] transition-colors"
        >
          <Filter size={12} strokeWidth={1.75} />
          Reset filters
        </button>
      )}
    </aside>
  );
}

function RailButton({
  active,
  onClick,
  icon,
  label,
  count,
}: {
  active: boolean;
  onClick: () => void;
  icon?: React.ReactNode;
  label: React.ReactNode;
  count?: number;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={active}
      className={cn(
        "group flex items-center justify-between gap-2 rounded-sm px-3 py-1.5 type-caption transition-colors duration-150",
        active
          ? "bg-aurora-a-soft text-fg-primary shadow-[inset_0_1px_0_rgba(255,255,255,0.08)]"
          : "text-fg-muted hover:bg-white/[0.05] hover:text-fg-primary",
      )}
    >
      <span className="flex items-center gap-1.5">
        {icon}
        {label}
      </span>
      {typeof count === "number" && (
        <span className="type-caption tabular-nums text-fg-disabled group-hover:text-fg-muted">
          {count}
        </span>
      )}
    </button>
  );
}

function RailCheckbox({
  active,
  onClick,
  label,
  count,
}: {
  active: boolean;
  onClick: () => void;
  label: string;
  count: number;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      role="checkbox"
      aria-checked={active}
      className={cn(
        "group flex items-center gap-2 rounded-sm px-3 py-1.5 type-caption transition-colors duration-150",
        "text-fg-muted hover:bg-white/[0.05] hover:text-fg-primary",
      )}
    >
      <span
        className={cn(
          "flex h-3.5 w-3.5 items-center justify-center rounded-[4px] border",
          active
            ? "bg-aurora-cyan/20 border-aurora-cyan/60 text-aurora-cyan"
            : "border-white/[0.14]",
        )}
      >
        {active && <Check size={10} strokeWidth={3} />}
      </span>
      <span className="flex-1 truncate font-mono">{label}</span>
      <span className="type-caption tabular-nums text-fg-disabled">{count}</span>
    </button>
  );
}
