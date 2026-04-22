"use client";

import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";

interface UnpublishDialogProps {
  open: boolean;
  onClose: () => void;
  agentName: string;
  importCount: number;
  onConfirm: () => Promise<void>;
}

export function UnpublishDialog({
  open,
  onClose,
  agentName,
  importCount,
  onConfirm,
}: UnpublishDialogProps) {
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const submit = async () => {
    if (busy) return;
    setBusy(true);
    setErr(null);
    try {
      await onConfirm();
      onClose();
    } catch (e) {
      setErr(e instanceof Error ? e.message : "Unpublish failed");
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title={`Unpublish ${agentName}?`}
      description="The catalog entry stays, but new users won't be able to discover it."
      footer={
        <>
          <Button variant="ghost" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button variant="danger" onClick={submit} disabled={busy}>
            {busy ? "Unpublishing…" : "Unpublish"}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-3 type-body text-fg-secondary">
        <ul className="list-inside space-y-1 rounded-sm bg-white/[0.03] p-3 type-caption text-fg-muted">
          <li>The catalog entry disappears from browse results.</li>
          <li>
            {importCount === 0
              ? "No users have imported this agent yet."
              : `${importCount} existing import${importCount === 1 ? "" : "s"} will see a deprecated banner but keep working.`}
          </li>
          <li>Your linked working copy will be unlinked — re-publish to reconnect.</li>
        </ul>
        {err && (
          <p className="type-caption text-danger" role="alert">
            {err}
          </p>
        )}
      </div>
    </Dialog>
  );
}
