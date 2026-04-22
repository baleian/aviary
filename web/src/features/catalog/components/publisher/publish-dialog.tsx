"use client";

import { useEffect, useMemo, useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { agentsApi } from "@/features/agents/api/agents-api";
import { catalogApi } from "@/features/catalog/api/catalog-api";
import type { Agent } from "@/types/agent";
import type {
  AgentVersion,
  CatalogCategory,
  DriftResponse,
} from "@/types/catalog";

const CATEGORY_OPTIONS: CatalogCategory[] = [
  "coding",
  "research",
  "writing",
  "productivity",
  "marketing",
  "data",
  "ops",
  "other",
];

interface PublishDialogProps {
  open: boolean;
  onClose: () => void;
  agentId: string;
  agentName: string;
  /** Current drift snapshot — passed in so caller controls freshness. */
  drift: DriftResponse | null;
  onPublished: () => void;
}

type DiffFieldRow = {
  field: string;
  label: string;
  before: string;
  after: string;
};

const DIFF_LABELS: Record<string, string> = {
  name: "Name",
  description: "Description",
  icon: "Icon",
  category: "Category",
  instruction: "Instruction",
  model_config_json: "Model",
  tools: "Built-in tools",
  mcp_servers: "MCP servers",
  mcp_tool_bindings: "MCP tool bindings",
};

function renderValue(v: unknown): string {
  if (v === null || v === undefined || v === "") return "—";
  if (typeof v === "string") return v;
  try {
    return JSON.stringify(v);
  } catch {
    return String(v);
  }
}

function truncate(s: string, n = 140): string {
  return s.length > n ? s.slice(0, n - 1) + "…" : s;
}

export function PublishDialog({
  open,
  onClose,
  agentId,
  agentName,
  drift,
  onPublished,
}: PublishDialogProps) {
  const [category, setCategory] = useState<CatalogCategory>("coding");
  const [notes, setNotes] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [composing, setComposing] = useState(false);

  const nextVersion = (drift?.latest_version_number ?? 0) + 1;
  const nothingToPublish =
    drift !== null && drift.is_dirty === false && nextVersion > 1;

  const [latestSnapshot, setLatestSnapshot] = useState<AgentVersion | null>(null);
  const [currentAgent, setCurrentAgent] = useState<Agent | null>(null);

  useEffect(() => {
    if (!open) {
      setError(null);
      setNotes("");
      return;
    }
    // Pull both snapshots for the diff preview.
    let cancelled = false;
    void (async () => {
      try {
        const [agent, latest] = await Promise.all([
          agentsApi.get(agentId),
          drift?.latest_version_id
            ? catalogApi
                .detail(
                  // The drift response doesn't carry catalog_id; fetch agent
                  // detail first to get `linked_catalog_agent_id`.
                  (await agentsApi.get(agentId)).linked_catalog_agent_id ?? "",
                  drift.latest_version_id,
                )
                .then((d) => d.version)
            : Promise.resolve(null),
        ]);
        if (cancelled) return;
        setCurrentAgent(agent);
        setLatestSnapshot(latest);
      } catch {
        /* diff is best-effort; dialog still works without it */
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [open, agentId, drift?.latest_version_id]);

  const diffRows: DiffFieldRow[] = useMemo(() => {
    if (!drift || !latestSnapshot || !currentAgent) return [];
    const pick = (field: string): [unknown, unknown] => {
      switch (field) {
        case "name": return [latestSnapshot.name, currentAgent.name];
        case "description":
          return [latestSnapshot.description, currentAgent.description];
        case "icon": return [latestSnapshot.icon, currentAgent.icon];
        case "instruction":
          return [latestSnapshot.instruction, currentAgent.instruction];
        case "category":
          return [latestSnapshot.category, category];
        case "model_config_json":
          return [latestSnapshot.model_config, currentAgent.model_config];
        case "tools":
          return [latestSnapshot.tools, currentAgent.tools];
        case "mcp_servers":
          return [latestSnapshot.mcp_servers, currentAgent.mcp_servers];
        default:
          return ["—", "—"];
      }
    };
    return drift.changed_fields
      .filter((f) => f !== "*" && f !== "mcp_tool_bindings")
      .map((field) => {
        const [before, after] = pick(field);
        return {
          field,
          label: DIFF_LABELS[field] ?? field,
          before: truncate(renderValue(before)),
          after: truncate(renderValue(after)),
        };
      });
  }, [drift, latestSnapshot, currentAgent, category]);

  const onSubmit = async () => {
    if (busy || nothingToPublish) return;
    setBusy(true);
    setError(null);
    try {
      await catalogApi.publish(agentId, {
        category,
        release_notes: notes.trim() || null,
      });
      onPublished();
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Publish failed");
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title={`Publish ${agentName}`}
      description={
        nextVersion === 1
          ? "This agent has never been published. Releasing v1 makes it visible to everyone in the catalog."
          : `Snapshot current state as v${nextVersion} and advance the catalog's latest pointer.`
      }
      footer={
        <>
          <Button variant="ghost" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button
            variant="cta"
            onClick={onSubmit}
            disabled={busy || nothingToPublish}
          >
            {busy ? "Publishing…" : `Publish v${nextVersion}`}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-5">
        {/* Category */}
        <div>
          <Label htmlFor="publish-category" className="mb-1.5 block">
            Category
          </Label>
          <div className="flex flex-wrap gap-1.5">
            {CATEGORY_OPTIONS.map((c) => {
              const active = c === category;
              return (
                <button
                  type="button"
                  key={c}
                  onClick={() => setCategory(c)}
                  aria-pressed={active}
                  className={
                    "rounded-sm px-2.5 py-1 type-caption-bold capitalize transition-colors " +
                    (active
                      ? "bg-aurora-a-soft text-fg-primary border border-aurora-violet/40"
                      : "bg-white/[0.03] text-fg-muted border border-white/[0.06] hover:bg-white/[0.06] hover:text-fg-secondary")
                  }
                >
                  {c}
                </button>
              );
            })}
          </div>
        </div>

        {/* Release notes */}
        <div>
          <Label htmlFor="release-notes" className="mb-1.5 block">
            Release notes <span className="text-fg-disabled">(optional)</span>
          </Label>
          <Textarea
            id="release-notes"
            rows={4}
            value={notes}
            placeholder="What changed? Add a short note for future-you or your team."
            onCompositionStart={() => setComposing(true)}
            onCompositionEnd={() => setComposing(false)}
            onChange={(e) => setNotes(e.target.value)}
            onKeyDown={(e) => {
              if (composing || e.nativeEvent.isComposing) return;
              if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
                e.preventDefault();
                void onSubmit();
              }
            }}
          />
          <p className="mt-1 type-caption text-fg-disabled">
            ⌘↵ to publish
          </p>
        </div>

        {drift && drift.changed_fields.length > 0 && drift.changed_fields[0] !== "*" && (
          <div className="rounded-sm border border-white/[0.06] bg-white/[0.02] p-3">
            <div className="mb-2 type-caption-bold uppercase tracking-wide text-fg-muted">
              Changed since v{drift.latest_version_number}
            </div>
            {diffRows.length === 0 ? (
              <div className="flex flex-wrap gap-1.5">
                {drift.changed_fields.map((f) => (
                  <code
                    key={f}
                    className="rounded-sm bg-white/[0.04] px-1.5 py-0.5 type-code-sm text-fg-secondary"
                  >
                    {DIFF_LABELS[f] ?? f}
                  </code>
                ))}
              </div>
            ) : (
              <ul className="flex flex-col gap-2">
                {diffRows.map((row) => (
                  <li
                    key={row.field}
                    className="rounded-sm bg-white/[0.02] p-2 type-caption"
                  >
                    <div className="type-caption-bold text-fg-secondary">
                      {row.label}
                    </div>
                    <div className="mt-1 grid gap-0.5">
                      <div className="text-danger/80">
                        <span className="text-fg-disabled">−&nbsp;</span>
                        <span className="font-mono">{row.before}</span>
                      </div>
                      <div className="text-success">
                        <span className="text-fg-disabled">+&nbsp;</span>
                        <span className="font-mono">{row.after}</span>
                      </div>
                    </div>
                  </li>
                ))}
                {drift.changed_fields.includes("mcp_tool_bindings") && (
                  <li className="rounded-sm bg-white/[0.02] p-2 type-caption text-fg-muted">
                    MCP tool bindings changed.
                  </li>
                )}
              </ul>
            )}
          </div>
        )}

        {nothingToPublish && (
          <p className="type-caption text-fg-muted">
            Nothing changed since v{drift?.latest_version_number}. Edit the
            agent before publishing a new version.
          </p>
        )}

        {error && (
          <p className="type-caption text-danger" role="alert">
            {error}
          </p>
        )}
      </div>
    </Dialog>
  );
}
