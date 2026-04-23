"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { Sparkles, TrendingUp, X } from "@/components/icons";
import { Skeleton } from "@/components/ui/skeleton";
import { catalogApi } from "@/features/catalog/api/catalog-api";
import { CatalogFeatured } from "@/features/catalog/components/catalog-featured";
import { CatalogGrid } from "@/features/catalog/components/catalog-grid";
import { CatalogHeroBanner } from "@/features/catalog/components/catalog-hero-banner";
import { CatalogScrollRow } from "@/features/catalog/components/catalog-scroll-row";
import { CategoryTiles } from "@/features/catalog/components/category-tiles";
import { useAuth } from "@/features/auth/providers/auth-provider";
import { cn } from "@/lib/utils";
import type {
  CatalogAgentSummary,
  CatalogFacets,
} from "@/types/catalog";

type SortKey = "recent" | "popular" | "name";

const SORT_OPTIONS: { value: SortKey; label: string }[] = [
  { value: "recent", label: "Recently published" },
  { value: "popular", label: "Most imported" },
  { value: "name", label: "Name (A–Z)" },
];

export default function CatalogPage() {
  const { user } = useAuth();
  const router = useRouter();
  const searchParams = useSearchParams();

  const urlQ = searchParams.get("q") ?? "";
  const urlCat = searchParams.get("cat") ?? "";
  const urlServers = useMemo(
    () => (searchParams.get("srv") ?? "").split(",").filter(Boolean),
    [searchParams],
  );
  const urlSort = (searchParams.get("sort") as SortKey | null) ?? "recent";

  const [searchInput, setSearchInput] = useState(urlQ);
  const [composing, setComposing] = useState(false);
  const [items, setItems] = useState<CatalogAgentSummary[]>([]);
  const [total, setTotal] = useState<number | null>(null);
  const [loading, setLoading] = useState(true);
  const [facets, setFacets] = useState<CatalogFacets | null>(null);
  const [importedMap, setImportedMap] = useState<Record<string, string>>({});

  const searchTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(() => {
    if (composing) return;
    if (searchInput === urlQ) return;
    if (searchTimer.current) clearTimeout(searchTimer.current);
    searchTimer.current = setTimeout(() => {
      const sp = new URLSearchParams(searchParams.toString());
      if (searchInput) sp.set("q", searchInput);
      else sp.delete("q");
      router.replace(`/catalog?${sp.toString()}`);
    }, 300);
    return () => {
      if (searchTimer.current) clearTimeout(searchTimer.current);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [searchInput, composing]);

  useEffect(() => {
    if (!user) return;
    setLoading(true);
    catalogApi
      .list({
        q: urlQ || undefined,
        category: urlCat ? [urlCat] : undefined,
        mcp_server: urlServers.length ? urlServers : undefined,
        sort: urlSort,
        limit: 100,
      })
      .then((data) => {
        setItems(data.items);
        setTotal(data.total);
      })
      .finally(() => setLoading(false));
  }, [user, urlQ, urlCat, urlServers, urlSort]);

  useEffect(() => {
    if (!user) return;
    catalogApi
      .facets({
        q: urlQ || undefined,
        category: urlCat ? [urlCat] : undefined,
        mcp_server: urlServers.length ? urlServers : undefined,
      })
      .then(setFacets);
  }, [user, urlQ, urlCat, urlServers]);

  const refreshImportedMap = useCallback(async () => {
    if (!user) return;
    try {
      const data = await catalogApi.listImports();
      const next: Record<string, string> = {};
      for (const a of data.items) {
        if (a.catalog_import_id && a.linked_catalog_agent_id) {
          next[a.linked_catalog_agent_id] = a.id;
        }
      }
      setImportedMap(next);
    } catch {
      /* server upsert is idempotent, button stays "Import" */
    }
  }, [user]);
  useEffect(() => {
    void refreshImportedMap();
  }, [refreshImportedMap]);

  const updateParam = useCallback(
    (updater: (sp: URLSearchParams) => void) => {
      const sp = new URLSearchParams(searchParams.toString());
      updater(sp);
      router.replace(`/catalog?${sp.toString()}`);
    },
    [router, searchParams],
  );

  const onCategory = (c: string | null) =>
    updateParam((sp) => {
      if (c) sp.set("cat", c);
      else sp.delete("cat");
    });

  const onToggleServer = (server: string) => {
    const next = urlServers.includes(server)
      ? urlServers.filter((s) => s !== server)
      : [...urlServers, server];
    updateParam((sp) => {
      if (next.length) sp.set("srv", next.join(","));
      else sp.delete("srv");
    });
  };

  const onReset = () =>
    updateParam((sp) => {
      sp.delete("cat");
      sp.delete("srv");
      sp.delete("q");
      sp.delete("sort");
    });

  const onSort = (v: SortKey) =>
    updateParam((sp) => {
      if (v === "recent") sp.delete("sort");
      else sp.set("sort", v);
    });

  const onImport = async (item: CatalogAgentSummary) => {
    try {
      await catalogApi.importAgents({
        items: [{ catalog_agent_id: item.id }],
      });
      await refreshImportedMap();
    } catch (err) {
      console.error("import failed", err);
    }
  };

  const hasAdvancedFilters = Boolean(
    urlQ || urlServers.length || urlSort !== "recent",
  );
  const showCurated = !hasAdvancedFilters;
  const hasActiveFilters = Boolean(urlCat) || hasAdvancedFilters;

  const featured = useMemo(
    () => [...items].sort((a, b) => b.import_count - a.import_count).slice(0, 2),
    [items],
  );
  const mostImported = useMemo(
    () =>
      [...items]
        .sort((a, b) => b.import_count - a.import_count)
        .slice(0, 12)
        .filter((x) => x.import_count > 0),
    [items],
  );
  const recent = useMemo(
    () =>
      [...items]
        .sort((a, b) => b.updated_at.localeCompare(a.updated_at))
        .slice(0, 12),
    [items],
  );

  const publishersCount = useMemo(
    () => new Set(items.map((i) => i.owner_email)).size,
    [items],
  );

  return (
    <div className="h-full overflow-y-auto bg-canvas">
      <CatalogHeroBanner
        total={total}
        categoriesCount={facets?.categories.length ?? null}
        publishersCount={items.length ? publishersCount : null}
        searchInput={searchInput}
        onSearchChange={setSearchInput}
        onCompositionStart={() => setComposing(true)}
        onCompositionEnd={(v) => {
          setComposing(false);
          setSearchInput(v);
        }}
      />

      <div className="mx-auto max-w-[1600px] px-6 pb-20 md:px-10">
        <section className="mt-8">
          <CategoryTiles
            facets={facets?.categories ?? []}
            selected={urlCat || null}
            onSelect={onCategory}
          />
        </section>

        {showCurated && !loading && items.length > 0 && (
          <div className="mt-12 flex flex-col gap-14">
            <CatalogFeatured
              items={featured}
              importedMap={importedMap}
              onImport={onImport}
            />
            {mostImported.length > 0 && (
              <CatalogScrollRow
                icon={
                  <TrendingUp
                    size={14}
                    strokeWidth={1.75}
                    className="text-aurora-coral"
                  />
                }
                label="Most imported"
                items={mostImported}
                importedMap={importedMap}
                onImport={onImport}
                action={
                  <button
                    type="button"
                    onClick={() => onSort("popular")}
                    className="type-caption text-fg-muted hover:text-fg-primary transition-colors"
                  >
                    See all →
                  </button>
                }
              />
            )}
            <CatalogScrollRow
              icon={
                <Sparkles
                  size={14}
                  strokeWidth={1.75}
                  className="text-aurora-coral"
                />
              }
              label="Recently published"
              items={recent}
              importedMap={importedMap}
              onImport={onImport}
              action={
                <button
                  type="button"
                  onClick={() => onSort("recent")}
                  className="type-caption text-fg-muted hover:text-fg-primary transition-colors"
                >
                  See all →
                </button>
              }
            />
          </div>
        )}

        {!showCurated && (
          <div className="mt-10">
            <FilterBar
              urlQ={urlQ}
              urlCat={urlCat}
              urlServers={urlServers}
              urlSort={urlSort}
              facets={facets}
              total={total}
              loading={loading}
              onCategory={onCategory}
              onToggleServer={onToggleServer}
              onReset={onReset}
              onSort={onSort}
              onClearQ={() => {
                setSearchInput("");
                updateParam((sp) => sp.delete("q"));
              }}
              resultCount={items.length}
            />
            <div className="mt-6">
              <CatalogGrid
                items={items}
                loading={loading}
                searchActive={hasActiveFilters}
                importedMap={importedMap}
                onImport={onImport}
                emptyAction={
                  <button
                    type="button"
                    onClick={onReset}
                    className="inline-flex items-center gap-1.5 type-caption-bold text-aurora-violet hover:opacity-90"
                  >
                    Reset all filters →
                  </button>
                }
              />
            </div>
          </div>
        )}

        {loading && showCurated && (
          <div className="mt-12 space-y-12">
            <div className="grid gap-4 lg:grid-cols-2">
              <Skeleton className="h-[220px] rounded-xl" />
              <Skeleton className="h-[220px] rounded-xl" />
            </div>
            <div className="flex gap-3 overflow-hidden">
              {Array.from({ length: 5 }).map((_, i) => (
                <Skeleton key={i} className="h-[196px] w-[280px] shrink-0 rounded-lg" />
              ))}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

function FilterBar({
  urlQ,
  urlCat,
  urlServers,
  urlSort,
  facets,
  total,
  loading,
  onCategory,
  onToggleServer,
  onReset,
  onSort,
  onClearQ,
  resultCount,
}: {
  urlQ: string;
  urlCat: string;
  urlServers: string[];
  urlSort: SortKey;
  facets: CatalogFacets | null;
  total: number | null;
  loading: boolean;
  onCategory: (c: string | null) => void;
  onToggleServer: (s: string) => void;
  onReset: () => void;
  onSort: (v: SortKey) => void;
  onClearQ: () => void;
  resultCount: number;
}) {
  const servers = facets?.servers ?? [];
  return (
    <div className="rounded-xl glass-pane p-4 md:p-5">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="type-caption text-fg-muted tabular-nums">
          {loading ? (
            <Skeleton className="h-3 w-24" />
          ) : (
            <>
              <span className="type-body font-semibold text-fg-primary">
                {resultCount}
              </span>
              <span className="ml-1 text-fg-muted">
                of {total ?? 0} results
              </span>
            </>
          )}
        </div>
        <label className="inline-flex items-center gap-2 type-caption text-fg-muted">
          Sort
          <select
            value={urlSort}
            onChange={(e) => onSort(e.target.value as SortKey)}
            className="rounded-sm border border-white/[0.08] bg-white/[0.04] px-2 py-1 type-caption text-fg-secondary focus-visible:outline-none focus-visible:border-aurora-coral/50"
          >
            {SORT_OPTIONS.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </select>
        </label>
      </div>

      {servers.length > 0 && (
        <div className="mt-4 flex flex-wrap items-center gap-1.5">
          <span className="type-caption text-fg-disabled mr-1">MCP servers:</span>
          {servers.map((s) => {
            const active = urlServers.includes(s.name);
            return (
              <button
                key={s.name}
                type="button"
                onClick={() => onToggleServer(s.name)}
                aria-pressed={active}
                className={cn(
                  "inline-flex items-center gap-1 rounded-pill border px-2.5 py-0.5 type-caption transition-all duration-150 font-mono",
                  active
                    ? "border-aurora-coral/40 bg-aurora-c-soft text-fg-primary"
                    : "border-white/[0.08] bg-white/[0.03] text-fg-muted hover:border-white/[0.14] hover:text-fg-primary",
                )}
              >
                {s.name}
                <span className="tabular-nums text-fg-disabled">· {s.count}</span>
              </button>
            );
          })}
        </div>
      )}

      {(urlQ || urlCat || urlServers.length) && (
        <div className="mt-4 flex flex-wrap items-center gap-1.5 border-t border-white/[0.06] pt-3">
          <span className="type-caption text-fg-disabled mr-1">Active:</span>
          {urlQ && <ActiveChip label={`"${urlQ}"`} onRemove={onClearQ} />}
          {urlCat && (
            <ActiveChip
              label={`Category: ${urlCat}`}
              onRemove={() => onCategory(null)}
            />
          )}
          {urlServers.map((s) => (
            <ActiveChip
              key={s}
              label={`Server: ${s}`}
              onRemove={() => onToggleServer(s)}
            />
          ))}
          <button
            type="button"
            onClick={onReset}
            className="ml-2 type-caption text-fg-muted hover:text-fg-primary transition-colors"
          >
            Reset all
          </button>
        </div>
      )}
    </div>
  );
}

function ActiveChip({ label, onRemove }: { label: string; onRemove: () => void }) {
  return (
    <span className="inline-flex items-center gap-1 rounded-pill bg-aurora-a-soft px-2.5 py-0.5 type-caption-bold text-fg-secondary border border-aurora-violet/30">
      {label}
      <button
        type="button"
        onClick={onRemove}
        aria-label={`Remove ${label}`}
        className="-mr-0.5 ml-0.5 rounded-full p-0.5 text-fg-muted hover:text-fg-primary hover:bg-white/[0.08] transition-colors"
      >
        <X size={10} strokeWidth={2.5} />
      </button>
    </span>
  );
}
