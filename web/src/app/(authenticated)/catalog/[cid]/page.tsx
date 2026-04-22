"use client";

import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { useParams, useRouter, useSearchParams } from "next/navigation";
import { ArrowLeft, Download, Check } from "@/components/icons";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { LoadingState } from "@/components/feedback/loading-state";
import { catalogApi } from "@/features/catalog/api/catalog-api";
import { VersionDropdown } from "@/features/catalog/components/detail/version-dropdown";
import { routes } from "@/lib/constants/routes";
import type {
  AgentVersionSummary,
  CatalogAgentDetail,
} from "@/types/catalog";

export default function CatalogDetailPage() {
  const params = useParams<{ cid: string }>();
  const router = useRouter();
  const searchParams = useSearchParams();
  const selectedVersion = searchParams.get("v");

  const [detail, setDetail] = useState<CatalogAgentDetail | null>(null);
  const [versions, setVersions] = useState<AgentVersionSummary[]>([]);
  const [loading, setLoading] = useState(true);
  const [importing, setImporting] = useState(false);

  useEffect(() => {
    setLoading(true);
    Promise.all([
      catalogApi.detail(params.cid, selectedVersion ?? undefined),
      catalogApi.versions(params.cid),
    ])
      .then(([d, v]) => {
        setDetail(d);
        setVersions(v.items);
      })
      .finally(() => setLoading(false));
  }, [params.cid, selectedVersion]);

  const onSelectVersion = useCallback(
    (versionId: string | null) => {
      const sp = new URLSearchParams(searchParams.toString());
      if (versionId) sp.set("v", versionId);
      else sp.delete("v");
      router.replace(`/catalog/${params.cid}?${sp.toString()}`);
    },
    [router, params.cid, searchParams],
  );

  const onImport = async () => {
    if (!detail) return;
    setImporting(true);
    try {
      const resp = await catalogApi.importAgents({
        items: [{ catalog_agent_id: detail.id }],
      });
      const created = resp.created[0] ?? resp.already_imported[0];
      if (created) {
        router.push(routes.agent(created.id));
      } else {
        // refresh to reflect imported state
        const fresh = await catalogApi.detail(detail.id);
        setDetail(fresh);
      }
    } finally {
      setImporting(false);
    }
  };

  if (loading || !detail) {
    return (
      <div className="h-full">
        <LoadingState label="Loading catalog entry…" />
      </div>
    );
  }

  const v = detail.version;
  const isDeprecated =
    detail.unpublished_at !== null && detail.unpublished_at !== undefined;
  const viewingNonLatest =
    selectedVersion && selectedVersion !== detail.current_version_id;

  return (
    <div className="h-full overflow-y-auto bg-canvas">
      <div className="mx-auto max-w-container px-6 pb-16 md:px-10">
        {/* Breadcrumb */}
        <div className="pt-6 pb-4">
          <Link
            href={routes.catalog}
            className="inline-flex items-center gap-1.5 type-caption text-fg-muted hover:text-fg-primary transition-colors"
          >
            <ArrowLeft size={12} strokeWidth={1.75} />
            Catalog
          </Link>
        </div>

        {isDeprecated && (
          <div className="mb-6 flex items-start gap-3 rounded-md border border-warning/30 bg-warning/[0.08] px-4 py-3 animate-fade-in-fast">
            <div className="mt-1 h-2 w-2 shrink-0 rounded-full bg-warning ring-2 ring-warning/20" />
            <div className="type-caption text-fg-secondary">
              This agent is no longer published. Existing imports continue to
              work, and you can still import a pinned historical version.
            </div>
          </div>
        )}

        {/* Hero */}
        <section className="grid gap-6 lg:grid-cols-[1fr_280px]">
          <div>
            <div className="flex items-start gap-4">
              <div className="flex h-14 w-14 shrink-0 items-center justify-center rounded-md bg-aurora-a-soft border border-white/[0.08] text-2xl">
                {v.icon || "🤖"}
              </div>
              <div className="min-w-0 flex-1">
                <h1 className="type-heading text-fg-primary">{v.name}</h1>
                <div className="mt-1 flex flex-wrap items-center gap-2 type-caption text-fg-muted">
                  <span>By {detail.owner_email}</span>
                  <span className="text-fg-disabled">·</span>
                  <VersionDropdown
                    versions={versions}
                    currentVersionId={detail.current_version_id ?? undefined}
                    latestVersionId={detail.current_version_id ?? undefined}
                    selectedVersionId={selectedVersion || null}
                    onSelect={onSelectVersion}
                  />
                </div>
                {v.description && (
                  <p className="mt-4 max-w-2xl type-body text-fg-secondary">
                    {v.description}
                  </p>
                )}
                <div className="mt-5 flex flex-wrap gap-2">
                  <Badge variant="neutral" className="capitalize">{v.category}</Badge>
                  {v.mcp_tool_bindings.map((b) => (
                    <Badge
                      key={`${b.server_name}-${b.tool_name}`}
                      variant="neutral"
                      className="font-mono"
                    >
                      {b.server_name}/{b.tool_name}
                    </Badge>
                  ))}
                </div>
              </div>
            </div>

            {viewingNonLatest && (
              <div className="mt-4 inline-flex items-center gap-2 rounded-sm border border-info/30 bg-info/[0.08] px-3 py-1 type-caption text-fg-secondary">
                Viewing v{v.version_number}.{" "}
                <button
                  type="button"
                  onClick={() => onSelectVersion(null)}
                  className="underline underline-offset-2 hover:text-aurora-cyan"
                >
                  Show latest
                </button>
              </div>
            )}

            {/* Instruction */}
            <section className="mt-10 rounded-lg glass-pane p-5">
              <h2 className="mb-3 type-caption-bold uppercase tracking-wide text-fg-muted">
                Instruction
              </h2>
              <pre className="max-h-[420px] overflow-auto whitespace-pre-wrap rounded-sm bg-black/30 p-4 type-code-sm text-fg-secondary">
                {v.instruction}
              </pre>
            </section>

            {/* Model + tools */}
            <section className="mt-6 grid gap-4 sm:grid-cols-2">
              <div className="rounded-lg glass-pane p-5">
                <h2 className="mb-3 type-caption-bold uppercase tracking-wide text-fg-muted">
                  Model
                </h2>
                <p className="type-body text-fg-secondary">
                  <span className="text-fg-muted">{v.model_config.backend}/</span>
                  {v.model_config.model}
                </p>
                {v.model_config.max_output_tokens && (
                  <p className="mt-1 type-caption text-fg-disabled">
                    max_output_tokens: {v.model_config.max_output_tokens}
                  </p>
                )}
              </div>
              <div className="rounded-lg glass-pane p-5">
                <h2 className="mb-3 type-caption-bold uppercase tracking-wide text-fg-muted">
                  Built-in tools
                </h2>
                {v.tools.length === 0 ? (
                  <p className="type-caption text-fg-disabled">None</p>
                ) : (
                  <div className="flex flex-wrap gap-1.5">
                    {v.tools.map((t) => (
                      <Badge key={t} variant="neutral" className="font-mono">
                        {t}
                      </Badge>
                    ))}
                  </div>
                )}
              </div>
            </section>
          </div>

          {/* Sticky CTA column */}
          <aside className="lg:sticky lg:top-8 lg:self-start">
            <div className="flex flex-col gap-3 rounded-lg glass-pane p-5">
              {detail.imported_as_agent_id ? (
                <Link
                  href={routes.agent(detail.imported_as_agent_id)}
                  className="inline-flex h-10 items-center justify-center gap-2 rounded-pill bg-aurora-cyan/15 border border-aurora-cyan/40 text-aurora-cyan type-button transition-colors hover:bg-aurora-cyan/20"
                >
                  <Check size={14} strokeWidth={2} />
                  Already imported
                </Link>
              ) : (
                <Button
                  variant="cta"
                  onClick={onImport}
                  disabled={importing || isDeprecated}
                  className="justify-center"
                >
                  <Download size={14} strokeWidth={2} />
                  {importing ? "Importing…" : "Import to my workspace"}
                </Button>
              )}

              <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1.5 type-caption">
                <dt className="text-fg-disabled">Version</dt>
                <dd className="text-fg-secondary">
                  v{v.version_number}
                  {detail.current_version_id === v.id && (
                    <span className="ml-1 text-aurora-cyan">· latest</span>
                  )}
                </dd>
                <dt className="text-fg-disabled">Published</dt>
                <dd className="text-fg-secondary">
                  {new Date(v.published_at).toLocaleDateString()}
                </dd>
                <dt className="text-fg-disabled">Imports</dt>
                <dd className="text-fg-secondary">{detail.import_count}</dd>
              </dl>

              {v.release_notes && (
                <div className="mt-1 rounded-sm bg-white/[0.03] p-3 type-caption text-fg-secondary">
                  <div className="mb-1 type-caption-bold uppercase tracking-wide text-fg-muted">
                    Release notes
                  </div>
                  <p className="whitespace-pre-wrap">{v.release_notes}</p>
                </div>
              )}
            </div>
          </aside>
        </section>
      </div>
    </div>
  );
}
