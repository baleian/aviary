"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { Send, Square } from "@/components/icons";
import { Kbd } from "@/components/ui/kbd";
import { MentionAutocomplete } from "./mention-autocomplete";

interface ChatInputProps {
  onSend: (content: string) => void;
  onCancel?: () => void;
  disabled?: boolean;
  isStreaming?: boolean;
  placeholder?: string;
  agentId?: string;
}

/**
 * ChatInput — auto-resizing textarea with send/cancel button.
 *
 * Send button is replaced by a square "stop" button while streaming.
 * MentionAutocomplete handles its own keyboard events when open.
 */
export function ChatInput({
  onSend,
  onCancel,
  disabled,
  isStreaming,
  placeholder,
  agentId,
}: ChatInputProps) {
  const [value, setValue] = useState("");
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const mentionOpenRef = useRef(false);

  const handleMentionOpenChange = useCallback((open: boolean) => {
    mentionOpenRef.current = open;
  }, []);

  // Auto-resize textarea (max 200px)
  useEffect(() => {
    const el = textareaRef.current;
    if (!el) return;
    el.style.height = "0";
    el.style.height = `${Math.min(el.scrollHeight, 200)}px`;
  }, [value]);

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    const content = value.trim();
    if (!content || disabled) return;
    onSend(content);
    setValue("");
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (mentionOpenRef.current) return;
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      handleSubmit(e);
    }
  };

  return (
    <form onSubmit={handleSubmit} className="relative">
      <div className="flex items-end gap-2 rounded-xl bg-elevated shadow-2 p-2 transition-shadow focus-within:glow-info">
        <textarea
          ref={textareaRef}
          value={value}
          onChange={(e) => setValue(e.target.value)}
          onKeyDown={handleKeyDown}
          placeholder={placeholder || "Send a message…"}
          className="max-h-[200px] min-h-[44px] flex-1 resize-none bg-transparent px-3 py-2.5 type-body text-fg-primary placeholder:text-fg-disabled focus:outline-none disabled:opacity-40"
          rows={1}
          disabled={disabled}
        />

        {isStreaming ? (
          <button
            type="button"
            onClick={onCancel}
            className="flex h-10 w-10 shrink-0 items-center justify-center rounded-md bg-danger/15 text-danger hover:bg-danger/25 transition-colors"
            aria-label="Stop generation"
          >
            <Square size={16} strokeWidth={2} fill="currentColor" />
          </button>
        ) : (
          <button
            type="submit"
            disabled={disabled || !value.trim()}
            className="flex h-10 w-10 shrink-0 items-center justify-center rounded-md bg-info text-canvas hover:opacity-80 transition-opacity disabled:opacity-30 disabled:cursor-not-allowed"
            aria-label="Send message"
          >
            <Send size={16} strokeWidth={2} />
          </button>
        )}
      </div>

      <MentionAutocomplete
        textareaRef={textareaRef}
        value={value}
        onChange={setValue}
        excludeAgentId={agentId}
        onOpenChange={handleMentionOpenChange}
      />

      <p className="mt-1.5 px-2 type-caption text-fg-disabled flex items-center gap-1.5">
        <Kbd>↵</Kbd>
        <span>to send,</span>
        <Kbd>⇧</Kbd>
        <Kbd>↵</Kbd>
        <span>for new line, type</span>
        <Kbd>@</Kbd>
        <span>to mention an agent.</span>
      </p>
    </form>
  );
}
