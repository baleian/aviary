"use client";

import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { catalogApi } from "@/features/catalog/api/catalog-api";
import type { CatalogCategory, DriftResponse } from "@/types/catalog";

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

  useEffect(() => {
    if (!open) {
      setError(null);
      setNotes("");
    }
  }, [open]);

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

        {/* Drift summary */}
        {drift && drift.changed_fields.length > 0 && drift.changed_fields[0] !== "*" && (
          <div className="rounded-sm border border-white/[0.06] bg-white/[0.02] p-3">
            <div className="mb-1 type-caption-bold uppercase tracking-wide text-fg-muted">
              Changed since v{drift.latest_version_number}
            </div>
            <div className="flex flex-wrap gap-1.5">
              {drift.changed_fields.map((f) => (
                <code
                  key={f}
                  className="rounded-sm bg-white/[0.04] px-1.5 py-0.5 type-code-sm text-fg-secondary"
                >
                  {f}
                </code>
              ))}
            </div>
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
