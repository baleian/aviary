"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { Search, Store } from "@/components/icons";
import { Input } from "@/components/ui/input";
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
  const urlSort =
    (searchParams.get("sort") as "recent" | "popular" | "name" | null) ??
    "recent";

  const [searchInput, setSearchInput] = useState(urlQ);
  const [items, setItems] = useState<CatalogAgentSummary[]>([]);
  const [total, setTotal] = useState<number | null>(null);
  const [loading, setLoading] = useState(true);
  const [facets, setFacets] = useState<CatalogFacets | null>(null);
  const [importedMap, setImportedMap] = useState<Record<string, string>>({});

  // Sync search input to URL with 300ms debounce.
  const searchTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(() => {
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
  }, [searchInput]);

  // Fetch the catalog list whenever filters change.
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

  // Fetch facets (less often — only when q/category change).
  useEffect(() => {
    if (!user) return;
    catalogApi
      .facets({
        q: urlQ || undefined,
        category: urlCat ? [urlCat] : undefined,
      })
      .then(setFacets);
  }, [user, urlQ, urlCat]);

  // Fetch which catalog ids the user has already imported so the button
  // renders as "Imported".
  const refreshImportedMap = useCallback(async () => {
    if (!user) return;
    try {
      const resp = await fetch("/api/agents?limit=200", {
        credentials: "include",
      });
      if (!resp.ok) return;
      const data: { items: Array<{ id: string; linked_catalog_agent_id?: string | null; catalog_import_id?: string | null }> } =
        await resp.json();
      const next: Record<string, string> = {};
      for (const a of data.items) {
        if (a.catalog_import_id && a.linked_catalog_agent_id) {
          next[a.linked_catalog_agent_id] = a.id;
        }
      }
      setImportedMap(next);
    } catch {
      // silent — the Import button will just show "Import" instead of
      // "Imported", still clickable (idempotent).
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

  const onCategory = (c: string | null) => {
    updateParam((sp) => {
      if (c) sp.set("cat", c);
      else sp.delete("cat");
    });
  };

  const onToggleServer = (server: string) => {
    const next = urlServers.includes(server)
      ? urlServers.filter((s) => s !== server)
      : [...urlServers, server];
    updateParam((sp) => {
      if (next.length) sp.set("srv", next.join(","));
      else sp.delete("srv");
    });
  };

  const onReset = () => {
    updateParam((sp) => {
      sp.delete("cat");
      sp.delete("srv");
    });
  };

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

  return (
    <div className="h-full overflow-y-auto bg-canvas">
      <div className="mx-auto max-w-container px-6 pb-16 md:px-10">
        {/* Top-right: link to my catalog (manage mine) */}
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

        {/* Search */}
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
              className="pl-9"
            />
          </div>
        </div>

        {/* Filter rail + grid */}
        <div className="grid gap-6 lg:grid-cols-[260px_1fr]">
          <CatalogFilterRail
            facets={facets}
            selectedCategory={urlCat || null}
            selectedServers={urlServers}
            onCategory={onCategory}
            onToggleServer={onToggleServer}
            onReset={onReset}
          />

          <div>
            <div className="mb-3 flex items-center justify-between">
              <div className="type-caption text-fg-muted">
                {loading ? " " : `${items.length} of ${total ?? 0}`}
              </div>
            </div>

            <CatalogGrid
              items={items}
              loading={loading}
              searchActive={Boolean(urlQ || urlCat || urlServers.length)}
              importedMap={importedMap}
              onImport={onImport}
            />
          </div>
        </div>
      </div>
    </div>
  );
}
