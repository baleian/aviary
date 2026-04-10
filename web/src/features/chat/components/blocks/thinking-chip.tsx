"use client";

import { memo, useCallback, useState } from "react";
import { ChevronRight } from "@/components/icons";
import { cn } from "@/lib/utils";

interface ThinkingChipProps {
  content: string;
  /** Active = currently streaming. Default-open while active. */
  isActive?: boolean;
  /** Used by SavedThinkingChip to start collapsed. */
  defaultOpen?: boolean;
}

/**
 * ThinkingChip — collapsible "thinking" block. Shows bouncing dots
 * indicator when streaming and collapsed.
 */
export const ThinkingChip = memo(function ThinkingChip({
  content,
  isActive = false,
  defaultOpen = true,
}: ThinkingChipProps) {
  const [isOpen, setIsOpen] = useState(defaultOpen);
  const toggle = useCallback(() => setIsOpen((v) => !v), []);

  return (
    <div className="rounded-xl rounded-tl-sm bg-elevated shadow-2 overflow-hidden">
      <button
        type="button"
        onClick={toggle}
        className="flex w-full items-center gap-2 px-4 py-2.5 text-left"
      >
        <ChevronRight
          size={12}
          strokeWidth={2}
          className={cn(
            "shrink-0 text-warning transition-transform duration-200",
            isOpen && "rotate-90",
          )}
        />
        <span className="type-caption text-warning">Thinking</span>
        {isActive && !isOpen && (
          <span className="flex items-center gap-1 ml-1">
            {[0, 150, 300].map((d) => (
              <span
                key={d}
                className="h-1 w-1 animate-bounce rounded-full bg-warning/60"
                style={{ animationDelay: `${d}ms`, animationDuration: "0.6s" }}
              />
            ))}
          </span>
        )}
      </button>
      {isOpen && (
        <div className="border-t border-white/[0.06] px-4 py-3">
          <div className="max-h-60 overflow-y-auto type-caption leading-relaxed text-fg-muted whitespace-pre-wrap break-words">
            {content}
          </div>
        </div>
      )}
    </div>
  );
});
