"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { ChevronDown, Check } from "@/components/icons";
import { cn } from "@/lib/utils";
import type { AgentVersionSummary } from "@/types/catalog";

// A "Latest" row renders first and maps to onSelect(null); below it comes
// each AgentVersion. Keyboard arrows walk this combined list, so the state
// type carries the kind tag instead of exposing two indices.
interface VersionDropdownProps {
  versions: AgentVersionSummary[];
  currentVersionId?: string | null;
  selectedVersionId: string | null;
  latestVersionId?: string | null;
  onSelect: (versionId: string | null) => void;
}

type Row =
  | { kind: "latest"; latestVersion: AgentVersionSummary | null }
  | { kind: "version"; version: AgentVersionSummary };

export function VersionDropdown({
  versions,
  currentVersionId,
  selectedVersionId,
  latestVersionId,
  onSelect,
}: VersionDropdownProps) {
  const [open, setOpen] = useState(false);
  const [activeIdx, setActiveIdx] = useState(0);
  const ref = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);

  const rows: Row[] = useMemo(() => {
    const latestVersion =
      (latestVersionId
        ? versions.find((v) => v.id === latestVersionId)
        : null) ?? null;
    const out: Row[] = [{ kind: "latest", latestVersion }];
    for (const v of versions) out.push({ kind: "version", version: v });
    return out;
  }, [versions, latestVersionId]);

  const selectedRowIndex = useMemo(() => {
    if (selectedVersionId === null) return 0;
    return (
      1 + versions.findIndex((v) => v.id === selectedVersionId)
    );
  }, [selectedVersionId, versions]);

  useEffect(() => {
    if (open) setActiveIdx(Math.max(0, selectedRowIndex));
  }, [open, selectedRowIndex]);

  useEffect(() => {
    if (!open) return;
    const onClick = (e: MouseEvent) => {
      if (!ref.current?.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        setOpen(false);
        triggerRef.current?.focus();
      } else if (e.key === "ArrowDown") {
        e.preventDefault();
        setActiveIdx((i) => Math.min(rows.length - 1, i + 1));
      } else if (e.key === "ArrowUp") {
        e.preventDefault();
        setActiveIdx((i) => Math.max(0, i - 1));
      } else if (e.key === "Enter") {
        e.preventDefault();
        const row = rows[activeIdx];
        if (!row) return;
        if (row.kind === "latest") onSelect(null);
        else onSelect(row.version.id);
        setOpen(false);
        triggerRef.current?.focus();
      } else if (e.key === "Home") {
        e.preventDefault();
        setActiveIdx(0);
      } else if (e.key === "End") {
        e.preventDefault();
        setActiveIdx(rows.length - 1);
      }
    };
    document.addEventListener("mousedown", onClick);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onClick);
      document.removeEventListener("keydown", onKey);
    };
  }, [open, rows, activeIdx, onSelect]);

  const selectedVersion = selectedVersionId
    ? versions.find((v) => v.id === selectedVersionId)
    : versions.find((v) => v.id === currentVersionId);

  const label =
    selectedVersionId === null
      ? selectedVersion
        ? `Latest (v${selectedVersion.version_number})`
        : "Latest"
      : selectedVersion
        ? `v${selectedVersion.version_number}`
        : "Version";

  const listboxId = "version-dropdown-listbox";

  return (
    <div ref={ref} className="relative">
      <button
        ref={triggerRef}
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-controls={listboxId}
        className={cn(
          "inline-flex items-center gap-1.5 rounded-pill px-3 py-1 type-caption-bold",
          "glass-raised text-fg-secondary hover:text-fg-primary transition-colors",
          "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-aurora-violet/50",
        )}
      >
        {label}
        <ChevronDown size={12} strokeWidth={1.75} />
      </button>

      {open && (
        <div
          id={listboxId}
          role="listbox"
          aria-activedescendant={`version-opt-${activeIdx}`}
          className={cn(
            "absolute left-0 top-full z-30 mt-1 w-72 overflow-hidden",
            "rounded-md bg-popover border border-white/[0.08] shadow-5 py-1",
            "animate-fade-in-fast",
          )}
        >
          {rows.map((row, idx) => {
            const active = idx === activeIdx;
            const picked =
              (row.kind === "latest" && selectedVersionId === null) ||
              (row.kind === "version" && row.version.id === selectedVersionId);

            if (row.kind === "latest") {
              return (
                <button
                  key="__latest__"
                  id={`version-opt-${idx}`}
                  type="button"
                  role="option"
                  aria-selected={picked}
                  onMouseEnter={() => setActiveIdx(idx)}
                  onClick={() => {
                    onSelect(null);
                    setOpen(false);
                    triggerRef.current?.focus();
                  }}
                  className={cn(
                    "flex w-full items-center justify-between gap-2 px-3 py-2 text-left type-caption transition-colors",
                    active
                      ? "bg-aurora-a-soft text-fg-primary"
                      : "text-fg-muted",
                    picked ? "text-fg-primary" : "",
                  )}
                >
                  <div className="min-w-0">
                    <div className="flex items-center gap-1.5">
                      <span className="type-caption-bold">Latest</span>
                      {row.latestVersion && (
                        <span className="text-aurora-cyan">
                          (v{row.latestVersion.version_number})
                        </span>
                      )}
                    </div>
                    <div className="truncate type-caption text-fg-disabled">
                      Follow the catalog — updates automatically.
                    </div>
                  </div>
                  {picked && (
                    <Check size={12} strokeWidth={2.5} className="text-aurora-violet" />
                  )}
                </button>
              );
            }

            const v = row.version;
            return (
              <button
                key={v.id}
                id={`version-opt-${idx}`}
                type="button"
                role="option"
                aria-selected={picked}
                onMouseEnter={() => setActiveIdx(idx)}
                onClick={() => {
                  onSelect(v.id);
                  setOpen(false);
                  triggerRef.current?.focus();
                }}
                className={cn(
                  "flex w-full items-center justify-between gap-2 px-3 py-2 text-left type-caption transition-colors",
                  active
                    ? "bg-aurora-a-soft text-fg-primary"
                    : "text-fg-muted",
                )}
              >
                <div className="min-w-0">
                  <div className="flex items-center gap-1.5">
                    <span className="type-caption-bold">v{v.version_number}</span>
                    {v.unpublished_at && (
                      <span className="text-warning">retracted</span>
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
