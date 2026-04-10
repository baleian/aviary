"use client";

import { restoreBlocks } from "@/features/chat/lib/restore-blocks";
import { MarkdownContent } from "@/features/chat/components/markdown/markdown-content";
import { ToolCallCard } from "@/features/chat/components/blocks/tool-call-card";
import { ThinkingChip } from "@/features/chat/components/blocks/thinking-chip";
import { MessageCopyButton } from "./message-copy-button";
import type { Message } from "@/types";

interface AgentBubbleProps {
  message: Message;
}

/**
 * AgentBubble — historical agent message. If the message has saved
 * `blocks` metadata, restores the structured tree (text + thinking +
 * tool calls); otherwise falls back to plain markdown of the content field.
 */
export function AgentBubble({ message }: AgentBubbleProps) {
  const savedBlocks = message.metadata?.blocks as Array<Record<string, unknown>> | undefined;
  const hasBlocks = Array.isArray(savedBlocks) && savedBlocks.length > 0;
  const isCancelled = message.metadata?.cancelled === true;

  return (
    <div className="flex gap-3 group animate-fade-in">
      <div className="mt-0.5 flex h-8 w-8 shrink-0 items-center justify-center rounded-md bg-raised type-small text-fg-muted">
        AI
      </div>

      <div className="max-w-[75%] space-y-1.5">
        {hasBlocks ? (
          restoreBlocks(savedBlocks!, isCancelled).map((block) => {
            if (block.type === "tool_call") {
              return <ToolCallCard key={block.id} block={block} />;
            }
            if (block.type === "thinking") {
              return (
                <ThinkingChip key={block.id} content={block.content} defaultOpen={false} />
              );
            }
            return (
              <div
                key={block.id}
                className="rounded-xl rounded-tl-sm bg-elevated shadow-2 px-4 py-3"
              >
                <div className="markdown-body break-words type-body text-fg-secondary">
                  <MarkdownContent content={block.content} />
                </div>
              </div>
            );
          })
        ) : (
          <div className="rounded-xl rounded-tl-sm bg-elevated shadow-2 px-4 py-3">
            <div className="markdown-body break-words type-body text-fg-secondary">
              <MarkdownContent content={message.content} />
            </div>
          </div>
        )}

        {isCancelled && (
          <div className="rounded-md border border-warning/20 bg-warning/[0.04] px-3 py-1.5 type-caption text-warning">
            Cancelled by user
          </div>
        )}

        <div className="opacity-0 transition-opacity group-hover:opacity-100">
          <MessageCopyButton text={message.content} />
        </div>
      </div>
    </div>
  );
}
