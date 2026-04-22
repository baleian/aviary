"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { catalogApi } from "@/features/catalog/api/catalog-api";
import { routes } from "@/lib/constants/routes";
import type { Agent } from "@/types/agent";

interface ForkDialogProps {
  open: boolean;
  onClose: () => void;
  agent: Agent;
}

export function ForkDialog({ open, onClose, agent }: ForkDialogProps) {
  const router = useRouter();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const onConfirm = async () => {
    if (busy) return;
    setBusy(true);
    setError(null);
    try {
      await catalogApi.forkImport(agent.id);
      // agent id is preserved — just refresh the detail.
      router.refresh();
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Fork failed");
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title={`Fork ${agent.name}`}
      description="Detach from the catalog and turn this into a private, editable agent."
      footer={
        <>
          <Button variant="ghost" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button variant="cta" onClick={onConfirm} disabled={busy}>
            {busy ? "Forking…" : "Fork and open"}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-3 type-body text-fg-secondary">
        <p>
          The current catalog snapshot is copied into your private workspace.
          Your chat sessions stay attached to this agent — only the fact
          that it tracks the catalog changes.
        </p>
        <ul className="list-inside space-y-1 rounded-sm bg-white/[0.03] p-3 type-caption text-fg-muted">
          <li>Instruction, tools, model, and MCP bindings materialize locally.</li>
          <li>Your existing conversations keep working with no id change.</li>
          <li>Future catalog updates will <strong>not</strong> reach this agent.</li>
        </ul>
        {error && (
          <p className="type-caption text-danger" role="alert">
            {error}
          </p>
        )}
      </div>
    </Dialog>
  );
}
