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
  const catalogDeprecated =
    detail.unpublished_at !== null && detail.unpublished_at !== undefined;
  const versionDeprecated =
    v.unpublished_at !== null && v.unpublished_at !== undefined;
  const isDeprecated = catalogDeprecated || versionDeprecated;
  const viewingNonLatest =
    selectedVersion && selectedVersion !== detail.current_version_id;

  return (
    <div className="h-full overflow-y-auto bg-canvas">
      <section className="relative isolate overflow-hidden border-b border-white/[0.06]">
        <div
          aria-hidden
          className="pointer-events-none absolute inset-0 -z-10 bg-aurora-a opacity-[0.08]"
        />
        <div
          aria-hidden
          className="pointer-events-none absolute -left-32 top-0 -z-10 h-[380px] w-[380px] rounded-full bg-aurora-a opacity-[0.22] blur-3xl"
        />
        <div
          aria-hidden
          className="pointer-events-none absolute inset-x-0 bottom-0 -z-10 h-20 bg-gradient-to-b from-transparent to-canvas"
        />

        <div className="relative mx-auto max-w-[1600px] px-6 pt-6 md:px-10">
          <Link
            href={routes.catalog}
            className="inline-flex items-center gap-1.5 type-caption text-fg-muted hover:text-fg-primary transition-colors"
          >
            <ArrowLeft size={12} strokeWidth={1.75} />
            Catalog
          </Link>

          {isDeprecated && (
            <div className="mt-6 flex items-start gap-3 rounded-md border border-warning/30 bg-warning/[0.08] px-4 py-3 animate-fade-in-fast">
              <div className="mt-1 h-2 w-2 shrink-0 rounded-full bg-warning ring-2 ring-warning/20" />
              <div className="type-caption text-fg-secondary">
                {versionDeprecated && !catalogDeprecated
                  ? `v${v.version_number} has been retracted. Only users pinned to it can still import it; new imports should use the latest version.`
                  : "This agent is no longer published. Existing imports keep working, and pinned-version imports remain reachable."}
              </div>
            </div>
          )}

          <div className="mt-8 grid gap-8 pb-12 lg:grid-cols-[1fr_360px]">
            <div className="min-w-0">
              <div className="flex items-start gap-5">
                <div className="flex h-20 w-20 shrink-0 items-center justify-center rounded-xl bg-aurora-a-soft border border-white/[0.10] text-4xl shadow-[0_8px_32px_rgba(123,92,255,0.20)]">
                  {v.icon || "🤖"}
                </div>
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <Badge variant="neutral" className="capitalize">
                      {v.category}
                    </Badge>
                    <span className="inline-flex items-center gap-1.5 type-caption text-fg-muted">
                      <span className="h-1 w-1 rounded-full bg-fg-disabled" />
                      {detail.import_count} imports
                    </span>
                    <span className="inline-flex items-center gap-1.5 type-caption text-fg-muted">
                      <span className="h-1 w-1 rounded-full bg-fg-disabled" />
                      Updated{" "}
                      {new Date(detail.updated_at).toLocaleDateString()}
                    </span>
                  </div>
                  <h1 className="mt-2 type-display text-fg-primary">{v.name}</h1>
                  <div className="mt-2 flex flex-wrap items-center gap-2 type-body text-fg-muted">
                    <span>
                      By{" "}
                      <span className="text-fg-secondary">
                        {detail.owner_email}
                      </span>
                    </span>
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
                    <p className="mt-5 max-w-3xl type-body-lg text-fg-secondary">
                      {v.description}
                    </p>
                  )}
                </div>
              </div>

              {viewingNonLatest && (
                <div className="mt-5 inline-flex items-center gap-2 rounded-md border border-info/30 bg-info/[0.08] px-3 py-1 type-caption text-fg-secondary">
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
            </div>

            <aside className="lg:sticky lg:top-6 lg:self-start">
              <div className="flex flex-col gap-4 rounded-xl glass-pane p-6">
                {detail.imported_as_agent_id ? (
                  <Link
                    href={routes.agent(detail.imported_as_agent_id)}
                    className="inline-flex h-11 items-center justify-center gap-2 rounded-pill bg-aurora-cyan/15 border border-aurora-cyan/40 text-aurora-cyan type-button transition-colors hover:bg-aurora-cyan/20"
                  >
                    <Check size={14} strokeWidth={2} />
                    Already imported — Open
                  </Link>
                ) : (
                  <Button
                    variant="cta"
                    onClick={onImport}
                    disabled={importing || isDeprecated}
                    className="h-11 justify-center type-button"
                  >
                    <Download size={14} strokeWidth={2} />
                    {importing ? "Importing…" : "Import to my workspace"}
                  </Button>
                )}
                <p className="type-caption text-fg-muted">
                  A new agent is created in your workspace. It stays linked for
                  version updates — fork any time to take ownership.
                </p>

                <div className="h-px bg-white/[0.06]" />

                <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 type-caption">
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
                  <dd className="text-fg-secondary tabular-nums">
                    {detail.import_count}
                  </dd>
                  <dt className="text-fg-disabled">Author</dt>
                  <dd className="text-fg-secondary truncate">
                    {detail.owner_email}
                  </dd>
                </dl>

                {v.release_notes && (
                  <>
                    <div className="h-px bg-white/[0.06]" />
                    <div className="rounded-md bg-white/[0.03] p-3 type-caption text-fg-secondary">
                      <div className="mb-1 type-caption-bold uppercase tracking-wide text-fg-muted">
                        Release notes
                      </div>
                      <p className="whitespace-pre-wrap">{v.release_notes}</p>
                    </div>
                  </>
                )}
              </div>
            </aside>
          </div>
        </div>
      </section>

      <div className="mx-auto max-w-[1600px] px-6 pb-20 pt-10 md:px-10">
        <div className="grid gap-6 lg:grid-cols-[1fr_360px]">
          <div className="min-w-0 space-y-6">
            <section className="rounded-xl glass-pane p-6">
              <h2 className="mb-4 flex items-center gap-2 type-caption-bold uppercase tracking-wide text-fg-secondary">
                <span className="h-1.5 w-1.5 rounded-full bg-aurora-coral" />
                Instruction
              </h2>
              <pre className="max-h-[520px] overflow-auto whitespace-pre-wrap rounded-md bg-black/30 p-5 type-code-sm text-fg-secondary">
                {v.instruction}
              </pre>
            </section>

            <section className="grid gap-4 sm:grid-cols-2">
              <div className="rounded-xl glass-pane p-6">
                <h2 className="mb-4 flex items-center gap-2 type-caption-bold uppercase tracking-wide text-fg-secondary">
                  <span className="h-1.5 w-1.5 rounded-full bg-aurora-violet" />
                  Model
                </h2>
                <p className="type-body-lg text-fg-primary">
                  <span className="text-fg-muted">
                    {v.model_config.backend}/
                  </span>
                  {v.model_config.model}
                </p>
                {v.model_config.max_output_tokens && (
                  <p className="mt-2 type-caption text-fg-disabled">
                    max_output_tokens: {v.model_config.max_output_tokens}
                  </p>
                )}
              </div>
              <div className="rounded-xl glass-pane p-6">
                <h2 className="mb-4 flex items-center gap-2 type-caption-bold uppercase tracking-wide text-fg-secondary">
                  <span className="h-1.5 w-1.5 rounded-full bg-aurora-cyan" />
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

          <aside className="min-w-0">
            <section className="rounded-xl glass-pane p-6">
              <h2 className="mb-4 flex items-center gap-2 type-caption-bold uppercase tracking-wide text-fg-secondary">
                <span className="h-1.5 w-1.5 rounded-full bg-aurora-coral" />
                MCP tools ({v.mcp_tool_bindings.length})
              </h2>
              {v.mcp_tool_bindings.length === 0 ? (
                <p className="type-caption text-fg-disabled">None bound</p>
              ) : (
                <div className="flex flex-wrap gap-1.5">
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
              )}
            </section>
          </aside>
        </div>
      </div>
    </div>
  );
}
