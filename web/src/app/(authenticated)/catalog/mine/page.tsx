"use client";

import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { ArrowLeft, ArrowRight, Store } from "@/components/icons";
import { Skeleton } from "@/components/ui/skeleton";
import { EmptyState } from "@/components/feedback/empty-state";
import { catalogApi } from "@/features/catalog/api/catalog-api";
import { MyCatalogCard } from "@/features/catalog/components/publisher/my-catalog-card";
import { useAuth } from "@/features/auth/providers/auth-provider";
import { routes } from "@/lib/constants/routes";
import type { MyCatalogAgent } from "@/types/catalog";

export default function MyCatalogPage() {
  const { user } = useAuth();
  const [items, setItems] = useState<MyCatalogAgent[]>([]);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    if (!user) return;
    setLoading(true);
    try {
      const data = await catalogApi.mine();
      setItems(data.items);
    } finally {
      setLoading(false);
    }
  }, [user]);

  useEffect(() => {
    void load();
  }, [load]);

  const published = items.filter((i) => i.is_published);
  const unpublished = items.filter((i) => !i.is_published);

  return (
    <div className="h-full overflow-y-auto bg-canvas">
      <div className="mx-auto max-w-container px-6 pb-16 md:px-10">
        <div className="pt-6 pb-4">
          <Link
            href={routes.catalog}
            className="inline-flex items-center gap-1.5 type-caption text-fg-muted hover:text-fg-primary transition-colors"
          >
            <ArrowLeft size={12} strokeWidth={1.75} />
            Catalog
          </Link>
        </div>

        <header className="mb-8 flex flex-col gap-1">
          <h1 className="type-heading text-fg-primary">My catalog entries</h1>
          <p className="type-caption text-fg-muted">
            Manage agents you've published. Entries stay here even if you
            delete their private working copy — use "Open as working copy"
            to reconnect.
          </p>
        </header>

        {loading ? (
          <div className="grid gap-3 sm:grid-cols-2 md:grid-cols-3">
            {[...Array(3)].map((_, i) => (
              <Skeleton key={i} className="h-44 rounded-lg" />
            ))}
          </div>
        ) : items.length === 0 ? (
          <EmptyState
            icon={<Store size={22} strokeWidth={1.5} />}
            title="You haven't published any agents yet"
            description="Open any of your agents and hit Publish to put it in the catalog."
            action={
              <Link
                href={routes.agents}
                className="inline-flex h-10 items-center gap-2 rounded-pill bg-aurora-a px-5 type-button text-white animate-aurora-sheen shadow-[0_0_24px_rgba(123,92,255,0.35)] hover:-translate-y-[1px] transition-transform duration-[320ms] ease-[cubic-bezier(0.16,1,0.3,1)]"
              >
                Pick an agent to publish
                <ArrowRight size={14} strokeWidth={2} />
              </Link>
            }
          />
        ) : (
          <>
            {published.length > 0 && (
              <section>
                <h2 className="mb-3 type-caption-bold uppercase tracking-wide text-fg-muted">
                  Published
                </h2>
                <div className="grid gap-3 sm:grid-cols-2 md:grid-cols-3">
                  {published.map((item) => (
                    <MyCatalogCard key={item.id} item={item} onMutated={load} />
                  ))}
                </div>
              </section>
            )}
            {unpublished.length > 0 && (
              <section className={published.length > 0 ? "mt-10" : ""}>
                <h2 className="mb-3 type-caption-bold uppercase tracking-wide text-fg-muted">
                  Unpublished
                </h2>
                <div className="grid gap-3 sm:grid-cols-2 md:grid-cols-3">
                  {unpublished.map((item) => (
                    <MyCatalogCard key={item.id} item={item} onMutated={load} />
                  ))}
                </div>
              </section>
            )}
          </>
        )}
      </div>
    </div>
  );
}
