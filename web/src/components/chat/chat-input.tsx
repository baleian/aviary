"use client";

import { useState, useRef, useEffect, useCallback } from "react";
import { Button } from "@/components/ui/button";
import { MentionAutocomplete } from "@/components/shared/mention-autocomplete";

interface ChatInputProps {
  onSend: (content: string) => void;
  onCancel?: () => void;
  disabled?: boolean;
  isStreaming?: boolean;
  placeholder?: string;
  /** Agent ID of the current chat agent (excluded from @ mention list) */
  agentId?: string;
}

export function ChatInput({ onSend, onCancel, disabled, isStreaming, placeholder, agentId }: ChatInputProps) {
  const [value, setValue] = useState("");
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const mentionOpenRef = useRef(false);
  const handleMentionOpenChange = useCallback((open: boolean) => {
    mentionOpenRef.current = open;
  }, []);

  // Auto-resize textarea
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
    // When mention dropdown is open, let it handle Enter/Tab/Arrow keys
    if (mentionOpenRef.current) return;
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      handleSubmit(e);
    }
  };

  return (
    <form onSubmit={handleSubmit} className="relative">
      <div className="flex items-end gap-2 rounded-2xl border border-border/50 bg-card p-2 transition-colors focus-within:border-primary/30 focus-within:shadow-lg focus-within:shadow-primary/5">
        <textarea
          ref={textareaRef}
          value={value}
          onChange={(e) => setValue(e.target.value)}
          onKeyDown={handleKeyDown}
          placeholder={placeholder || "Send a message... (Shift+Enter for new line)"}
          className="max-h-[200px] min-h-[44px] flex-1 resize-none bg-transparent px-3 py-2.5 text-sm leading-relaxed text-foreground placeholder:text-muted-foreground/50 focus:outline-none disabled:opacity-40"
          rows={1}
          disabled={disabled}
        />
        {isStreaming ? (
          <Button
            type="button"
            onClick={onCancel}
            size="icon"
            variant="destructive"
            className="h-10 w-10 shrink-0 rounded-xl"
          >
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <rect x="6" y="6" width="12" height="12" rx="2" />
            </svg>
          </Button>
        ) : (
          <Button
            type="submit"
            disabled={disabled || !value.trim()}
            size="icon"
            className="h-10 w-10 shrink-0 rounded-xl"
          >
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <line x1="22" y1="2" x2="11" y2="13" />
              <polygon points="22 2 15 22 11 13 2 9 22 2" />
            </svg>
          </Button>
        )}
      </div>
      <MentionAutocomplete
        textareaRef={textareaRef}
        value={value}
        onChange={setValue}
        excludeAgentId={agentId}
        onOpenChange={handleMentionOpenChange}
      />
      <p className="mt-1.5 px-2 text-[10px] text-muted-foreground/40">
        Press Enter to send, Shift+Enter for new line. Type @ to mention an agent.
      </p>
    </form>
  );
}
