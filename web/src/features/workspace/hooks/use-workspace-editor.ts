"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  getFile,
  saveFile,
  type FileContents,
  WorkspaceApiError,
} from "../lib/workspace-api";

export interface EditorTab {
  path: string;
  loading: boolean;
  error: string | null;
  savedContent: string | null;
  savedMtime: number | null;
  draft: string | null;
  isBinary: boolean;
  size: number;
}

export type SaveResult =
  | { status: "saved" }
  | { status: "conflict"; currentMtime: number | null; currentSize: number | null; code: "stale" | "exists" }
  | { status: "error"; message: string }
  | { status: "noop" };

interface UseWorkspaceEditorResult {
  tabs: EditorTab[];
  activeTabPath: string | null;
  activeTab: EditorTab | null;
  openFile: (path: string) => Promise<void>;
  closeTab: (path: string) => void;
  activate: (path: string) => void;
  setDraft: (path: string, value: string) => void;
  save: (path: string, overrideOverwrite?: { expectedMtime: number | null }) => Promise<SaveResult>;
  reloadTab: (path: string) => Promise<void>;
  removeTab: (path: string) => void;
  renameTab: (oldPath: string, newPath: string) => void;
  hasDirty: boolean;
  isTabDirty: (path: string) => boolean;
}

const emptyTab = (path: string): EditorTab => ({
  path,
  loading: true,
  error: null,
  savedContent: null,
  savedMtime: null,
  draft: null,
  isBinary: false,
  size: 0,
});

export function useWorkspaceEditor(sessionId: string): UseWorkspaceEditorResult {
  const [tabs, setTabs] = useState<EditorTab[]>([]);
  const [activeTabPath, setActiveTabPath] = useState<string | null>(null);
  // per-path fetch generations so concurrent loads can't clobber each other
  const generationsRef = useRef<Map<string, number>>(new Map());

  useEffect(() => {
    setTabs([]);
    setActiveTabPath(null);
    generationsRef.current = new Map();
  }, [sessionId]);

  const patchTab = useCallback((path: string, patch: Partial<EditorTab>) => {
    setTabs((prev) =>
      prev.map((t) => (t.path === path ? { ...t, ...patch } : t)),
    );
  }, []);

  const applyFileContents = useCallback((path: string, file: FileContents) => {
    patchTab(path, {
      loading: false,
      error: null,
      savedContent: file.content,
      savedMtime: file.mtime,
      draft: null,
      isBinary: file.isBinary,
      size: file.size,
    });
  }, [patchTab]);

  const nextGeneration = (path: string): number => {
    const gens = generationsRef.current;
    const gen = (gens.get(path) ?? 0) + 1;
    gens.set(path, gen);
    return gen;
  };

  const loadFile = useCallback(
    async (path: string) => {
      const gen = nextGeneration(path);
      patchTab(path, { loading: true, error: null });
      try {
        const file = await getFile(sessionId, path);
        if (generationsRef.current.get(path) !== gen) return;
        applyFileContents(path, file);
      } catch (err) {
        if (generationsRef.current.get(path) !== gen) return;
        const message = err instanceof Error ? err.message : "Failed to load file";
        patchTab(path, { loading: false, error: message });
      }
    },
    [sessionId, patchTab, applyFileContents],
  );

  const openFile = useCallback(
    async (path: string) => {
      setTabs((prev) => {
        if (prev.some((t) => t.path === path)) return prev;
        return [...prev, emptyTab(path)];
      });
      setActiveTabPath(path);
      await loadFile(path);
    },
    [loadFile],
  );

  const removeTab = useCallback((path: string) => {
    setTabs((prev) => {
      const next = prev.filter((t) => t.path !== path);
      if (activeTabPath === path) {
        const idx = prev.findIndex((t) => t.path === path);
        const nextActive =
          next[idx] ?? next[idx - 1] ?? next[0] ?? null;
        setActiveTabPath(nextActive ? nextActive.path : null);
      }
      return next;
    });
    generationsRef.current.delete(path);
  }, [activeTabPath]);

  // closeTab defers the dirty-check to callers so they can show a confirm
  // dialog; this helper unconditionally removes.
  const closeTab = useCallback(
    (path: string) => {
      removeTab(path);
    },
    [removeTab],
  );

  const activate = useCallback((path: string) => {
    setActiveTabPath(path);
  }, []);

  const setDraft = useCallback((path: string, value: string) => {
    setTabs((prev) =>
      prev.map((t) => {
        if (t.path !== path) return t;
        const draft = value === t.savedContent ? null : value;
        return { ...t, draft };
      }),
    );
  }, []);

  const save = useCallback(
    async (
      path: string,
      override?: { expectedMtime: number | null },
    ): Promise<SaveResult> => {
      const tab = tabsRef.current.find((t) => t.path === path);
      if (!tab) return { status: "error", message: "Tab not found" };
      if (tab.draft === null && !override) return { status: "noop" };
      const content = tab.draft ?? tab.savedContent ?? "";
      const expectedMtime = override ? override.expectedMtime : tab.savedMtime;
      const overwrite = !!override;
      try {
        const file = await saveFile(sessionId, path, {
          content,
          encoding: "utf8",
          expectedMtime,
          overwrite,
        });
        applyFileContents(path, file);
        return { status: "saved" };
      } catch (err) {
        if (err instanceof WorkspaceApiError && err.status === 409 && (err.code === "stale" || err.code === "exists")) {
          return {
            status: "conflict",
            currentMtime: err.currentMtime,
            currentSize: err.currentSize,
            code: err.code,
          };
        }
        const message = err instanceof Error ? err.message : "Save failed";
        return { status: "error", message };
      }
    },
    [sessionId, applyFileContents],
  );

  // Keep a ref so `save` can read the latest draft without being re-created
  // on every keystroke (which would re-register monaco commands too).
  const tabsRef = useRef<EditorTab[]>(tabs);
  useEffect(() => {
    tabsRef.current = tabs;
  }, [tabs]);

  const reloadTab = useCallback(async (path: string) => {
    await loadFile(path);
  }, [loadFile]);

  const renameTab = useCallback((oldPath: string, newPath: string) => {
    setTabs((prev) => prev.map((t) => (t.path === oldPath ? { ...t, path: newPath } : t)));
    setActiveTabPath((cur) => (cur === oldPath ? newPath : cur));
    const gen = generationsRef.current.get(oldPath);
    if (gen !== undefined) {
      generationsRef.current.set(newPath, gen);
      generationsRef.current.delete(oldPath);
    }
  }, []);

  const activeTab = useMemo(
    () => tabs.find((t) => t.path === activeTabPath) ?? null,
    [tabs, activeTabPath],
  );

  const hasDirty = useMemo(() => tabs.some((t) => t.draft !== null), [tabs]);

  const isTabDirty = useCallback(
    (path: string) => {
      const t = tabs.find((x) => x.path === path);
      return !!t && t.draft !== null;
    },
    [tabs],
  );

  return {
    tabs,
    activeTabPath,
    activeTab,
    openFile,
    closeTab,
    activate,
    setDraft,
    save,
    reloadTab,
    removeTab,
    renameTab,
    hasDirty,
    isTabDirty,
  };
}
