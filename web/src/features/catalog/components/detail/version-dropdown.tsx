"use client";

import { useEffect, useRef, useState } from "react";
import { ChevronDown, Check } from "@/components/icons";
import { cn } from "@/lib/utils";
import type { AgentVersionSummary } from "@/types/catalog";

interface VersionDropdownProps {
  versions: AgentVersionSummary[];
  currentVersionId?: string | null;
  selectedVersionId: string | null;
  latestVersionId?: string | null;
  onSelect: (versionId: string | null) => void;
}

/**
 * Version selector — dropdown list. First row is "Latest" (selects null);
 * subsequent rows are each AgentVersion by number + date + notes preview.
 */
export function VersionDropdown({
  versions,
  currentVersionId,
  selectedVersionId,
  latestVersionId,
  onSelect,
}: VersionDropdownProps) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    function onClick(e: MouseEvent) {
      if (!ref.current?.contains(e.target as Node)) setOpen(false);
    }
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") setOpen(false);
    }
    document.addEventListener("mousedown", onClick);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onClick);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  const displayVersion = selectedVersionId
    ? versions.find((v) => v.id === selectedVersionId)
    : versions.find((v) => v.id === currentVersionId);

  const label =
    selectedVersionId === null
      ? displayVersion
        ? `v${displayVersion.version_number} · latest`
        : "Latest"
      : displayVersion
        ? `v${displayVersion.version_number}`
        : "Version";

  return (
    <div ref={ref} className="relative">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        aria-haspopup="listbox"
        className={cn(
          "inline-flex items-center gap-1.5 rounded-pill px-3 py-1 type-caption-bold",
          "glass-raised text-fg-secondary hover:text-fg-primary transition-colors",
        )}
      >
        {label}
        <ChevronDown size={12} strokeWidth={1.75} />
      </button>

      {open && (
        <div
          role="listbox"
          className={cn(
            "absolute left-0 top-full z-30 mt-1 w-64 overflow-hidden",
            "rounded-md bg-popover border border-white/[0.08] shadow-5 py-1",
            "animate-fade-in-fast",
          )}
        >
          {versions.map((v) => {
            const isLatest = v.id === latestVersionId;
            const picked =
              selectedVersionId === v.id ||
              (selectedVersionId === null && isLatest);
            return (
              <button
                key={v.id}
                type="button"
                role="option"
                aria-selected={picked}
                onClick={() => {
                  onSelect(isLatest ? null : v.id);
                  setOpen(false);
                }}
                className={cn(
                  "flex w-full items-center justify-between gap-2 px-3 py-2 text-left",
                  "type-caption transition-colors",
                  picked ? "bg-aurora-a-soft text-fg-primary" : "hover:bg-white/[0.05] text-fg-muted",
                )}
              >
                <div className="min-w-0">
                  <div className="flex items-center gap-1.5">
                    <span className="type-caption-bold">v{v.version_number}</span>
                    {isLatest && (
                      <span className="text-aurora-cyan">· latest</span>
                    )}
                  </div>
                  {v.release_notes && (
                    <div className="truncate type-caption text-fg-disabled">
                      {v.release_notes}
                    </div>
                  )}
                </div>
                {picked && (
                  <Check size={12} strokeWidth={2.5} className="text-aurora-violet" />
                )}
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}
