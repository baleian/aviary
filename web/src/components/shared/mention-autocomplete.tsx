"use client";

import { useState, useEffect, useRef, useCallback } from "react";
import { apiFetch } from "@/lib/api";
import type { Agent } from "@/types";

interface MentionItem {
  id: string;
  slug: string;
  name: string;
  description?: string;
}

interface MentionAutocompleteProps {
  /** The textarea element ref to attach mention behavior to */
  textareaRef: React.RefObject<HTMLTextAreaElement | null>;
  /** Current text value */
  value: string;
  /** Callback to update the text value */
  onChange: (value: string) => void;
  /** Slug of the current agent to exclude from the list (prevent self-mention) */
  excludeSlug?: string;
  /** ID of the current agent to exclude (alternative to excludeSlug) */
  excludeAgentId?: string;
  /** Called when the dropdown opens or closes (parent can suppress Enter-to-send) */
  onOpenChange?: (open: boolean) => void;
}

/**
 * Mention autocomplete overlay for textareas.
 * Detects "@" followed by typing, shows a dropdown of accessible agents,
 * and inserts the selected agent's @slug into the text.
 */
export function MentionAutocomplete({ textareaRef, value, onChange, excludeSlug, excludeAgentId, onOpenChange }: MentionAutocompleteProps) {
  const [isOpen, setIsOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [agents, setAgents] = useState<MentionItem[]>([]);
  const [filtered, setFiltered] = useState<MentionItem[]>([]);
  // Notify parent of actual visibility (open AND has results)
  const isVisible = isOpen && filtered.length > 0;
  useEffect(() => { onOpenChange?.(isVisible); }, [isVisible, onOpenChange]);
  const [selectedIndex, setSelectedIndex] = useState(0);
  const [mentionStart, setMentionStart] = useState(-1);
  const [position, setPosition] = useState({ top: 0, left: 0 });
  const dropdownRef = useRef<HTMLDivElement>(null);
  const agentsLoaded = useRef(false);

  // Fetch agents list once
  const loadAgents = useCallback(async () => {
    if (agentsLoaded.current) return;
    try {
      const res = await apiFetch<{ items: Agent[] }>("/agents?limit=100");
      setAgents(
        res.items.map((a) => ({
          id: a.id,
          slug: a.slug,
          name: a.name,
          description: a.description,
        }))
      );
      agentsLoaded.current = true;
    } catch {
      // silently fail — user can still type without mentions
    }
  }, []);

  // Detect "@" trigger
  useEffect(() => {
    const textarea = textareaRef.current;
    if (!textarea) return;

    const handleInput = () => {
      const cursorPos = textarea.selectionStart;
      const text = textarea.value;

      // Look backward from cursor for @ that starts a mention
      let start = -1;
      for (let i = cursorPos - 1; i >= 0; i--) {
        const ch = text[i];
        if (ch === "@") {
          // Check that @ is at start of text or preceded by whitespace
          if (i === 0 || /\s/.test(text[i - 1])) {
            start = i;
          }
          break;
        }
        // Stop if we hit whitespace (not a valid mention)
        if (/\s/.test(ch)) break;
      }

      if (start >= 0) {
        const mentionQuery = text.slice(start + 1, cursorPos).toLowerCase();
        setMentionStart(start);
        setQuery(mentionQuery);
        setIsOpen(true);
        setSelectedIndex(0);
        loadAgents();

        // Position dropdown near cursor (approximate)
        const rect = textarea.getBoundingClientRect();
        const lineHeight = parseInt(getComputedStyle(textarea).lineHeight) || 20;
        const lines = text.slice(0, start).split("\n").length;
        setPosition({
          top: rect.top - Math.min(lines, 3) * lineHeight - 8,
          left: rect.left + 16,
        });
      } else {
        setIsOpen(false);
      }
    };

    textarea.addEventListener("input", handleInput);
    textarea.addEventListener("click", handleInput);
    return () => {
      textarea.removeEventListener("input", handleInput);
      textarea.removeEventListener("click", handleInput);
    };
  }, [textareaRef, loadAgents]);

  // Filter agents by query (excluding self)
  useEffect(() => {
    if (!isOpen) return;
    const q = query.toLowerCase();
    setFiltered(
      agents.filter(
        (a) =>
          a.slug !== excludeSlug &&
          a.id !== excludeAgentId &&
          (a.slug.includes(q) || a.name.toLowerCase().includes(q))
      ).slice(0, 8)
    );
  }, [query, agents, isOpen, excludeSlug, excludeAgentId]);

  // Handle keyboard navigation
  useEffect(() => {
    const textarea = textareaRef.current;
    if (!textarea || !isOpen) return;

    const handleKeyDown = (e: KeyboardEvent) => {
      if (filtered.length === 0) return; // No suggestions visible — let parent handle
      if (e.key === "ArrowDown") {
        e.preventDefault();
        e.stopPropagation();
        setSelectedIndex((i) => Math.min(i + 1, filtered.length - 1));
      } else if (e.key === "ArrowUp") {
        e.preventDefault();
        e.stopPropagation();
        setSelectedIndex((i) => Math.max(i - 1, 0));
      } else if (e.key === "Enter" || e.key === "Tab") {
        e.preventDefault();
        e.stopPropagation();
        selectAgent(filtered[selectedIndex]);
      } else if (e.key === "Escape") {
        e.stopPropagation();
        setIsOpen(false);
      }
    };

    textarea.addEventListener("keydown", handleKeyDown);
    return () => textarea.removeEventListener("keydown", handleKeyDown);
  }, [isOpen, filtered, selectedIndex, textareaRef]);

  const selectAgent = (agent: MentionItem) => {
    const textarea = textareaRef.current;
    if (!textarea) return;

    const before = value.slice(0, mentionStart);
    const after = value.slice(textarea.selectionStart);
    const newValue = `${before}@${agent.slug} ${after}`;
    onChange(newValue);
    setIsOpen(false);

    // Restore cursor position after the inserted mention
    requestAnimationFrame(() => {
      const newPos = mentionStart + agent.slug.length + 2; // @ + slug + space
      textarea.setSelectionRange(newPos, newPos);
      textarea.focus();
    });
  };

  // Close on click outside
  useEffect(() => {
    if (!isOpen) return;
    const handleClick = (e: MouseEvent) => {
      if (dropdownRef.current && !dropdownRef.current.contains(e.target as Node)) {
        setIsOpen(false);
      }
    };
    document.addEventListener("mousedown", handleClick);
    return () => document.removeEventListener("mousedown", handleClick);
  }, [isOpen]);

  if (!isOpen || filtered.length === 0) return null;

  return (
    <div
      ref={dropdownRef}
      className="fixed z-50 w-72 rounded-xl border border-border bg-popover shadow-xl overflow-hidden"
      style={{ bottom: `calc(100vh - ${position.top}px)`, left: position.left }}
    >
      <div className="px-3 py-2 text-[10px] font-medium uppercase tracking-wider text-muted-foreground/60 border-b border-border/50">
        Agents
      </div>
      <div className="max-h-48 overflow-y-auto py-1">
        {filtered.map((agent, i) => (
          <button
            key={agent.slug}
            type="button"
            className={`flex w-full items-start gap-3 px-3 py-2 text-left transition-colors ${
              i === selectedIndex ? "bg-primary/10 text-foreground" : "text-foreground/80 hover:bg-muted/50"
            }`}
            onMouseEnter={() => setSelectedIndex(i)}
            onClick={() => selectAgent(agent)}
          >
            <div className="flex h-7 w-7 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-xs font-bold text-primary">
              @
            </div>
            <div className="min-w-0 flex-1">
              <div className="text-sm font-medium truncate">{agent.slug}</div>
              {agent.description && (
                <div className="text-xs text-muted-foreground truncate">{agent.description}</div>
              )}
            </div>
          </button>
        ))}
      </div>
    </div>
  );
}
