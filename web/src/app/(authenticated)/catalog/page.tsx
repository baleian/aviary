"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { Filter, Search, Store, X } from "@/components/icons";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { catalogApi } from "@/features/catalog/api/catalog-api";
import { CatalogFilterRail } from "@/features/catalog/components/catalog-filter-rail";
import { CatalogGrid } from "@/features/catalog/components/catalog-grid";
import { CatalogHero } from "@/features/catalog/components/catalog-hero";
import { useAuth } from "@/features/auth/providers/auth-provider";
import { routes } from "@/lib/constants/routes";
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
  const [mobileRailOpen, setMobileRailOpen] = useState(false);

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

  const hasActiveFilters = Boolean(urlCat || urlServers.length);
  const activeFilterCount = (urlCat ? 1 : 0) + urlServers.length;

  return (
    <div className="h-full overflow-y-auto bg-canvas">
      <div className="mx-auto max-w-container px-6 pb-16 md:px-10">
        <div className="flex justify-end pt-6">
          <Link
            href={routes.catalogMine}
            className="inline-flex items-center gap-1.5 rounded-pill glass-raised px-3 py-1 type-caption text-fg-muted hover:text-fg-primary transition-colors"
          >
            <Store size={12} strokeWidth={1.75} className="text-aurora-coral" />
            My entries
          </Link>
        </div>

        <CatalogHero total={total} />

        <div className="mx-auto mt-2 mb-8 max-w-xl">
          <div className="relative">
            <Search
              size={14}
              strokeWidth={1.75}
              className="absolute left-3 top-1/2 -translate-y-1/2 text-fg-disabled pointer-events-none"
            />
            <Input
              placeholder="Search agents…"
              value={searchInput}
              onChange={(e) => setSearchInput(e.target.value)}
              onCompositionStart={() => setComposing(true)}
              onCompositionEnd={(e) => {
                setComposing(false);
                setSearchInput((e.target as HTMLInputElement).value);
              }}
              className="pl-9"
              aria-label="Search catalog"
            />
          </div>
        </div>

        <div className="grid gap-6 lg:grid-cols-[260px_1fr]">
          <div className="hidden lg:block">
            <CatalogFilterRail
              facets={facets}
              selectedCategory={urlCat || null}
              selectedServers={urlServers}
              onCategory={onCategory}
              onToggleServer={onToggleServer}
              onReset={onReset}
            />
          </div>

          <div>
            <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
              <div className="flex items-center gap-2 type-caption text-fg-muted">
                {loading ? (
                  <Skeleton className="h-3 w-24" />
                ) : (
                  <>{items.length} of {total ?? 0}</>
                )}
                <button
                  type="button"
                  onClick={() => setMobileRailOpen(true)}
                  className="inline-flex items-center gap-1.5 rounded-sm px-2 py-0.5 type-caption-bold text-fg-secondary hover:text-fg-primary hover:bg-white/[0.04] transition-colors lg:hidden"
                  aria-label="Open filters"
                >
                  <Filter size={12} strokeWidth={1.75} />
                  Filters{activeFilterCount ? ` (${activeFilterCount})` : ""}
                </button>
              </div>

              <label className="inline-flex items-center gap-1.5 type-caption text-fg-muted">
                Sort
                <select
                  value={urlSort}
                  onChange={(e) => onSort(e.target.value as SortKey)}
                  className="rounded-sm bg-white/[0.04] border border-white/[0.08] text-fg-secondary px-2 py-1 type-caption focus-visible:outline-none focus-visible:border-aurora-violet/50"
                >
                  {SORT_OPTIONS.map((o) => (
                    <option key={o.value} value={o.value}>{o.label}</option>
                  ))}
                </select>
              </label>
            </div>

            {hasActiveFilters && (
              <div className="mb-4 flex flex-wrap items-center gap-1.5">
                {urlCat && (
                  <FilterChip
                    label={`Category: ${urlCat}`}
                    onRemove={() => onCategory(null)}
                  />
                )}
                {urlServers.map((srv) => (
                  <FilterChip
                    key={srv}
                    label={`Server: ${srv}`}
                    onRemove={() => onToggleServer(srv)}
                  />
                ))}
                <button
                  type="button"
                  onClick={onReset}
                  className="type-caption text-fg-muted hover:text-fg-primary transition-colors"
                >
                  Reset all
                </button>
              </div>
            )}

            <CatalogGrid
              items={items}
              loading={loading}
              searchActive={Boolean(urlQ || urlCat || urlServers.length)}
              importedMap={importedMap}
              onImport={onImport}
              emptyAction={
                <Link
                  href={routes.agents}
                  className="inline-flex items-center gap-1.5 type-caption-bold text-aurora-violet hover:opacity-90"
                >
                  Go to your agents →
                </Link>
              }
            />
          </div>
        </div>
      </div>

      {mobileRailOpen && (
        <div
          className="fixed inset-0 z-40 flex lg:hidden"
          role="dialog"
          aria-modal="true"
          aria-label="Filters"
        >
          <button
            type="button"
            aria-label="Close filters"
            className="absolute inset-0 bg-black/60 backdrop-blur-[2px]"
            onClick={() => setMobileRailOpen(false)}
          />
          <aside className="relative ml-0 flex h-full w-[min(320px,85vw)] flex-col bg-canvas glass-pane p-4 animate-slide-in-left">
            <div className="mb-3 flex items-center justify-between">
              <h2 className="type-caption-bold uppercase tracking-wide text-fg-muted">
                Filters
              </h2>
              <Button
                variant="ghost"
                size="sm"
                onClick={() => setMobileRailOpen(false)}
                aria-label="Close filters"
              >
                <X size={14} strokeWidth={1.75} />
              </Button>
            </div>
            <CatalogFilterRail
              facets={facets}
              selectedCategory={urlCat || null}
              selectedServers={urlServers}
              onCategory={onCategory}
              onToggleServer={onToggleServer}
              onReset={onReset}
            />
          </aside>
        </div>
      )}
    </div>
  );
}

function FilterChip({
  label,
  onRemove,
}: {
  label: string;
  onRemove: () => void;
}) {
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
