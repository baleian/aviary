"use client";

import { AlertTriangle } from "@/components/icons";

interface ErrorBlockViewProps {
  message: string;
  targetId?: string;
}

export function ErrorBlockView({ message, targetId }: ErrorBlockViewProps) {
  return (
    <div
      data-search-target={targetId}
      className="rounded-md border border-danger/30 bg-danger/[0.06] px-3 py-2"
    >
      <div className="flex items-start gap-2">
        <AlertTriangle
          size={14}
          strokeWidth={2}
          className="mt-0.5 shrink-0 text-danger"
        />
        <div className="min-w-0 flex-1">
          <div className="type-caption-bold text-danger">Agent error</div>
          <pre className="mt-1 overflow-auto whitespace-pre-wrap break-words type-code-sm text-fg-secondary">
            {message}
          </pre>
        </div>
      </div>
    </div>
  );
}
