"use client";

import { memo } from "react";
import { TextBlockView } from "./text-block";
import { ThinkingChip } from "./thinking-chip";
import { ToolCallCard } from "./tool-call-card";
import { ActivityIndicator } from "./activity-indicator";
import type { StreamBlock } from "@/types";

interface StreamingResponseProps {
  blocks: StreamBlock[];
  isStreaming: boolean;
}

/**
 * StreamingResponse — renders the live agent message-in-progress.
 * Each block type dispatches to its own dedicated component.
 */
export const StreamingResponse = memo(function StreamingResponse({
  blocks,
  isStreaming,
}: StreamingResponseProps) {
  return (
    <div className="flex gap-3 animate-fade-in">
      {/* Avatar */}
      <div className="mt-0.5 flex h-8 w-8 shrink-0 items-center justify-center rounded-md bg-raised type-small text-fg-muted">
        AI
      </div>

      <div className="min-w-0 max-w-[75%] space-y-2">
        {blocks.map((block, idx) => {
          const isLast = idx === blocks.length - 1;

          if (block.type === "thinking") {
            return (
              <ThinkingChip
                key={block.id}
                content={block.content}
                isActive={isLast && isStreaming}
              />
            );
          }
          if (block.type === "text") {
            return <TextBlockView key={block.id} content={block.content} />;
          }
          if (block.type === "tool_call") {
            return <ToolCallCard key={block.id} block={block} />;
          }
          return null;
        })}

        {isStreaming && <ActivityIndicator />}
      </div>
    </div>
  );
});
