"use client";

import { cn } from "@/lib/utils";

type Variant = "published" | "draft" | "unpublished" | "drift";

interface PublishedStateChipProps {
  variant: Variant;
  versionNumber?: number | null;
  className?: string;
}

/**
 * Catalog state chip for an owner's working copy. Never doubles as a
 * version-number badge — use ``Badge variant="muted"`` for plain "v3".
 */
export function PublishedStateChip({
  variant,
  versionNumber,
  className,
}: PublishedStateChipProps) {
  let label: string;
  let dotClass: string;
  let textClass: string;

  switch (variant) {
    case "published":
      label = `Published v${versionNumber ?? "?"}`;
      dotClass = "bg-success";
      textClass = "text-fg-secondary";
      break;
    case "draft":
      label = "Draft";
      dotClass = "bg-fg-disabled";
      textClass = "text-fg-muted";
      break;
    case "unpublished":
      label = "Unpublished";
      dotClass = "bg-warning";
      textClass = "text-fg-secondary";
      break;
    case "drift":
      label = versionNumber
        ? `Unpublished changes · v${versionNumber}`
        : "Unpublished changes";
      dotClass = "bg-warning ring-2 ring-warning/20";
      textClass = "text-fg-secondary";
      break;
  }

  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 type-caption-bold",
        textClass,
        className,
      )}
    >
      <span className={cn("inline-block h-1.5 w-1.5 rounded-full", dotClass)} />
      {label}
    </span>
  );
}
