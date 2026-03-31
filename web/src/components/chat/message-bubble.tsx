"use client";

import { useCallback, useState } from "react";
import { cn } from "@/lib/utils";
import { MarkdownContent } from "@/components/chat/markdown-content";
import type { Message } from "@/types";

interface MessageBubbleProps {
  message: Message;
  currentUserId?: string;
}

function MessageCopyButton({ text }: { text: string }) {
  const [copied, setCopied] = useState(false);

  const handleCopy = useCallback(async () => {
    await navigator.clipboard.writeText(text);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  }, [text]);

  return (
    <button
      onClick={handleCopy}
      className="mt-1 flex items-center gap-1 rounded-md px-2 py-0.5 text-[11px] text-muted-foreground/60 transition-colors hover:text-muted-foreground"
      title="Copy message"
    >
      {copied ? (
        <>
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><polyline points="20 6 9 17 4 12"/></svg>
          Copied
        </>
      ) : (
        <>
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><rect x="9" y="9" width="13" height="13" rx="2" ry="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>
          Copy
        </>
      )}
    </button>
  );
}

export function MessageBubble({ message, currentUserId }: MessageBubbleProps) {
  const isUser = message.sender_type === "user";

  return (
    <div
      className={cn(
        "group flex gap-3 animate-fade-in",
        isUser ? "flex-row-reverse" : "flex-row"
      )}
    >
      {/* Avatar */}
      <div
        className={cn(
          "mt-0.5 flex h-8 w-8 shrink-0 items-center justify-center rounded-lg text-xs font-semibold",
          isUser
            ? "bg-chat-user/20 text-chat-user-fg"
            : "bg-secondary text-muted-foreground"
        )}
      >
        {isUser ? "You" : "AI"}
      </div>

      {/* Message content */}
      <div className={cn("max-w-[75%] space-y-1", isUser ? "items-end" : "items-start")}>
        <div
          className={cn(
            "rounded-2xl px-4 py-3 text-[14px] leading-[1.7]",
            isUser
              ? "bg-chat-user text-chat-user-fg rounded-tr-md"
              : "bg-chat-agent text-chat-agent-fg rounded-tl-md"
          )}
        >
          {isUser ? (
            <div className="whitespace-pre-wrap break-words">{message.content}</div>
          ) : (
            <div className="markdown-body break-words">
              <MarkdownContent content={message.content} />
            </div>
          )}
        </div>

        {/* Copy button (visible on hover) */}
        {!isUser && (
          <div className="opacity-0 transition-opacity group-hover:opacity-100">
            <MessageCopyButton text={message.content} />
          </div>
        )}

        {/* Tool calls */}
        {message.metadata && Object.keys(message.metadata).length > 0 && Array.isArray(message.metadata.tool_calls) && (
          <div className="space-y-1 px-1">
            {(message.metadata.tool_calls as Array<Record<string, unknown>>).map((tool, i) => (
              <div
                key={i}
                className="flex items-center gap-2 rounded-lg border border-border/30 bg-secondary/50 px-3 py-1.5 text-xs"
              >
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" className="shrink-0 text-muted-foreground">
                  <path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z" />
                </svg>
                <span className="font-mono text-muted-foreground">
                  {String(tool.name)}
                </span>
                <span className="truncate text-muted-foreground/60">
                  {JSON.stringify(tool.input).slice(0, 80)}
                </span>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
