"use client";

import { memo, useCallback, useEffect, useState } from "react";
import { MarkdownContent } from "@/components/chat/markdown-content";
import { ToolCallCard } from "@/components/chat/tool-call-card";
import type { StreamBlock } from "@/types";

interface StreamingResponseProps {
  blocks: StreamBlock[];
  isStreaming: boolean;
  lastEventAt: number;
}

/** Threshold (ms) — if no event arrives within this window, show "waiting" state */
const STALE_THRESHOLD_MS = 5_000;

const TextBlockView = memo(function TextBlockView({
  content,
  isLast,
  isStreaming,
}: {
  content: string;
  isLast: boolean;
  isStreaming: boolean;
}) {
  return (
    <div className="markdown-body break-words text-[14px] leading-[1.7] text-chat-agent-fg">
      <MarkdownContent content={content} />
      {isLast && isStreaming && (
        <span className="ml-0.5 inline-block h-4 w-0.5 animate-pulse-soft bg-primary" />
      )}
    </div>
  );
});

const ThinkingChip = memo(function ThinkingChip({
  content,
  isActive,
}: {
  content: string;
  isActive: boolean;
}) {
  const [isOpen, setIsOpen] = useState(true);
  const toggle = useCallback(() => setIsOpen((v) => !v), []);

  return (
    <div className="rounded-2xl rounded-tl-md bg-chat-agent">
      <button
        onClick={toggle}
        className="flex w-full items-center gap-2 px-4 py-2.5 text-left"
      >
        <svg
          width="14"
          height="14"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
          className={`shrink-0 text-amber-400 transition-transform duration-200 ${isOpen ? "rotate-90" : ""}`}
        >
          <polyline points="9 18 15 12 9 6" />
        </svg>
        <span className="text-[12px] font-medium text-amber-400/90">Thinking</span>
        {isActive && !isOpen && (
          <span className="flex items-center gap-1 ml-1">
            <span
              className="h-1 w-1 animate-bounce rounded-full bg-amber-400/60"
              style={{ animationDelay: "0ms", animationDuration: "0.6s" }}
            />
            <span
              className="h-1 w-1 animate-bounce rounded-full bg-amber-400/60"
              style={{ animationDelay: "150ms", animationDuration: "0.6s" }}
            />
            <span
              className="h-1 w-1 animate-bounce rounded-full bg-amber-400/60"
              style={{ animationDelay: "300ms", animationDuration: "0.6s" }}
            />
          </span>
        )}
      </button>
      {isOpen && (
        <div className="border-t border-border/20 px-4 py-3">
          <div className="max-h-60 overflow-y-auto text-[12px] leading-relaxed text-muted-foreground/70 whitespace-pre-wrap break-words">
            {content}
            {isActive && (
              <span className="ml-0.5 inline-block h-3 w-0.5 animate-pulse-soft bg-amber-400/50" />
            )}
          </div>
        </div>
      )}
    </div>
  );
});

function ActivityIndicator({ lastEventAt }: { lastEventAt: number }) {
  const [isStale, setIsStale] = useState(false);

  useEffect(() => {
    const check = () => {
      setIsStale(Date.now() - lastEventAt >= STALE_THRESHOLD_MS);
    };
    check();
    const timer = setInterval(check, 1_000);
    return () => clearInterval(timer);
  }, [lastEventAt]);

  return (
    <div className="rounded-2xl rounded-tl-md bg-chat-agent px-4 py-3">
      <div className="flex items-center gap-1.5">
        {isStale ? (
          <>
            <span
              className="h-1.5 w-1.5 animate-pulse-soft rounded-full bg-muted-foreground/50"
              style={{ animationDelay: "0ms" }}
            />
            <span
              className="h-1.5 w-1.5 animate-pulse-soft rounded-full bg-muted-foreground/50"
              style={{ animationDelay: "200ms" }}
            />
            <span
              className="h-1.5 w-1.5 animate-pulse-soft rounded-full bg-muted-foreground/50"
              style={{ animationDelay: "400ms" }}
            />
            <span className="ml-1.5 text-[11px] text-muted-foreground/50">
              waiting for model...
            </span>
          </>
        ) : (
          <>
            <span
              className="h-1.5 w-1.5 animate-bounce rounded-full bg-primary"
              style={{ animationDelay: "0ms", animationDuration: "0.6s" }}
            />
            <span
              className="h-1.5 w-1.5 animate-bounce rounded-full bg-primary"
              style={{ animationDelay: "150ms", animationDuration: "0.6s" }}
            />
            <span
              className="h-1.5 w-1.5 animate-bounce rounded-full bg-primary"
              style={{ animationDelay: "300ms", animationDuration: "0.6s" }}
            />
          </>
        )}
      </div>
    </div>
  );
}

export const StreamingResponse = memo(function StreamingResponse({
  blocks,
  isStreaming,
  lastEventAt,
}: StreamingResponseProps) {
  const lastBlock = blocks[blocks.length - 1];
  // Thinking chip is "active" (animating) when it's the last block and still streaming
  const isThinkingActive = isStreaming && lastBlock?.type === "thinking";
  // Show activity indicator when streaming and last block is neither text nor thinking
  const showActivity = isStreaming && blocks.length === 0;

  return (
    <div className="flex gap-3 animate-fade-in">
      {/* Avatar */}
      <div className="mt-0.5 flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-secondary text-xs font-semibold text-muted-foreground">
        AI
      </div>

      {/* Content */}
      <div className="min-w-0 max-w-[75%] space-y-2">
        {/* Blocks */}
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
            return (
              <div
                key={block.id}
                className="rounded-2xl rounded-tl-md bg-chat-agent px-4 py-3"
              >
                <TextBlockView
                  content={block.content}
                  isLast={isLast}
                  isStreaming={isStreaming}
                />
              </div>
            );
          }

          if (block.type === "tool_call") {
            return <ToolCallCard key={block.id} block={block} />;
          }

          return null;
        })}

        {/* Loading indicator: only when no blocks yet */}
        {showActivity && (
          <ActivityIndicator lastEventAt={lastEventAt} />
        )}
      </div>
    </div>
  );
});
