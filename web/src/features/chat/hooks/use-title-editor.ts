"use client";

import { useCallback, useRef, useState, type KeyboardEvent } from "react";
import { http } from "@/lib/http";
import type { Session } from "@/types";

interface UseTitleEditorOptions {
  session: Session | null;
  patchSession: (patch: Partial<Session>) => void;
}

export function useTitleEditor({ session, patchSession }: UseTitleEditorOptions) {
  const [isEditing, setIsEditing] = useState(false);
  const [draft, setDraft] = useState("");
  const inputRef = useRef<HTMLInputElement>(null);

  const startEditing = useCallback(() => {
    if (!session) return;
    setDraft(session.title || "");
    setIsEditing(true);
    setTimeout(() => inputRef.current?.focus(), 0);
  }, [session]);

  const saveTitle = useCallback(
    async (next: string) => {
      if (!session) return;
      const trimmed = next.trim();
      if (!trimmed || trimmed === session.title) return;

      const previousTitle = session.title;
      patchSession({ title: trimmed });
      try {
        await http.patch(`/sessions/${session.id}/title`, { title: trimmed });
      } catch (err) {
        patchSession({ title: previousTitle });
        throw err;
      }
    },
    [session, patchSession],
  );

  const save = useCallback(async () => {
    setIsEditing(false);
    await saveTitle(draft);
  }, [draft, saveTitle]);

  const handleKeyDown = useCallback((e: KeyboardEvent<HTMLInputElement>) => {
    if (e.nativeEvent.isComposing || e.keyCode === 229) return;
    if (e.key === "Enter") e.currentTarget.blur();
    else if (e.key === "Escape") setIsEditing(false);
  }, []);

  const setAutoTitleFromMessage = useCallback(
    (content: string) => {
      if (!session || session.title) return;
      const firstLine = content.trim().split("\n")[0];
      const autoTitle = firstLine.length > 60 ? firstLine.slice(0, 57) + "…" : firstLine;
      patchSession({ title: autoTitle });
    },
    [session, patchSession],
  );

  return {
    isEditing,
    draft,
    setDraft,
    inputRef,
    startEditing,
    save,
    saveTitle,
    handleKeyDown,
    setAutoTitleFromMessage,
  };
}
