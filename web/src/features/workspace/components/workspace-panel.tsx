"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { cn } from "@/lib/utils";
import { useWorkspaceTree } from "../hooks/use-workspace-tree";
import { useWorkspaceEditor } from "../hooks/use-workspace-editor";
import { usePanelResize } from "../hooks/use-panel-resize";
import {
  MAX_UPLOAD_BYTES,
  WorkspaceApiError,
  createDir,
  deleteEntry,
  downloadFileUrl,
  moveEntry,
  saveFile,
  uploadFile,
} from "../lib/workspace-api";
import { FileTree } from "./file-tree";
import { WorkspaceToolbar } from "./workspace-toolbar";
import { FileEditor } from "./file-editor";
import { EditorTabs } from "./editor-tabs";
import { ConflictDialog } from "./conflict-dialog";
import { ConfirmDialog } from "./confirm-dialog";
import {
  WorkspaceContextMenu,
  type ContextMenuItem,
} from "./workspace-context-menu";
import type { PendingNew, TreeInteractions } from "./tree-node";
import type { TreeEntry } from "../lib/workspace-api";

interface WorkspacePanelProps {
  sessionId: string;
  onClose: () => void;
  refreshSignal?: number;
}

const TREE_WIDTH_WITH_EDITOR = 360;
const TREE_ONLY_MIN_WIDTH = 240;
const TREE_ONLY_DEFAULT_WIDTH = 360;
const TREE_PLUS_EDITOR_MIN_WIDTH = TREE_WIDTH_WITH_EDITOR + 360;
const TREE_PLUS_EDITOR_DEFAULT_WIDTH = TREE_WIDTH_WITH_EDITOR + 1024;
const PANEL_MAX_WIDTH = 1800;
const CHAT_MIN_WIDTH = 320;
const STORAGE_KEY_COLLAPSED = "aviary:workspace-panel-width:collapsed";
const STORAGE_KEY_EXPANDED = "aviary:workspace-panel-width:expanded";

function parentOf(path: string): string {
  if (path === "/" || !path.includes("/")) return "/";
  const trimmed = path.replace(/\/+$/g, "");
  const idx = trimmed.lastIndexOf("/");
  return idx <= 0 ? "/" : trimmed.slice(0, idx);
}

function joinChild(parent: string, name: string): string {
  return parent === "/" ? `/${name}` : `${parent}/${name}`;
}

function basename(path: string): string {
  const slash = path.lastIndexOf("/");
  return slash === -1 ? path : path.slice(slash + 1);
}

type ContextMenuState = {
  x: number;
  y: number;
  items: ContextMenuItem[];
} | null;

type ConflictState = {
  path: string;
  code: "stale" | "exists";
  retryOverwrite: () => Promise<void>;
  onReload: () => Promise<void>;
} | null;

type ConfirmState = {
  title: string;
  body: string;
  confirmLabel: string;
  danger?: boolean;
  onConfirm: () => void;
  thirdAction?: { label: string; onClick: () => void };
} | null;

export function WorkspacePanel({ sessionId, onClose, refreshSignal = 0 }: WorkspacePanelProps) {
  const tree = useWorkspaceTree(sessionId);
  const editor = useWorkspaceEditor(sessionId);
  const [refreshing, setRefreshing] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const [contextMenu, setContextMenu] = useState<ContextMenuState>(null);
  const [conflict, setConflict] = useState<ConflictState>(null);
  const [confirmState, setConfirmState] = useState<ConfirmState>(null);
  const [renamingPath, setRenamingPath] = useState<string | null>(null);
  const [pendingNew, setPendingNew] = useState<PendingNew>(null);

  const editorOpen = editor.activeTabPath !== null;

  const { width: panelWidth, isResizing, onMouseDown } = usePanelResize({
    storageKey: editorOpen ? STORAGE_KEY_EXPANDED : STORAGE_KEY_COLLAPSED,
    defaultWidth: editorOpen ? TREE_PLUS_EDITOR_DEFAULT_WIDTH : TREE_ONLY_DEFAULT_WIDTH,
    minWidth: editorOpen ? TREE_PLUS_EDITOR_MIN_WIDTH : TREE_ONLY_MIN_WIDTH,
    maxWidth: PANEL_MAX_WIDTH,
    reserveForMain: CHAT_MIN_WIDTH,
  });

  // Warn on session change if any tab is dirty.
  const prevSessionRef = useRef(sessionId);
  useEffect(() => {
    if (prevSessionRef.current === sessionId) return;
    if (editor.hasDirty) {
      const ok = window.confirm(
        "You have unsaved changes in the workspace. Switching sessions will discard them. Continue?",
      );
      if (!ok) {
        // Can't really rewind — sessionId changed upstream. Still reset.
      }
    }
    prevSessionRef.current = sessionId;
  }, [sessionId, editor.hasDirty]);

  const refresh = useCallback(async () => {
    setRefreshing(true);
    try {
      await tree.refreshAll();
      if (editor.activeTabPath) await editor.reloadTab(editor.activeTabPath);
    } finally {
      setRefreshing(false);
    }
  }, [tree, editor]);

  const lastSignalRef = useRef<number>(refreshSignal);
  useEffect(() => {
    if (refreshSignal === lastSignalRef.current) return;
    lastSignalRef.current = refreshSignal;
    void refresh();
  }, [refreshSignal, refresh]);

  // --- Save flow with conflict handling -----------------------------------
  const doSave = useCallback(
    async (path: string, overrideExpected?: number | null) => {
      setSaving(true);
      setError(null);
      try {
        const result = await editor.save(
          path,
          overrideExpected !== undefined ? { expectedMtime: overrideExpected } : undefined,
        );
        if (result.status === "conflict") {
          setConflict({
            path,
            code: result.code,
            retryOverwrite: async () => {
              setConflict(null);
              await doSave(path, result.currentMtime);
            },
            onReload: async () => {
              setConflict(null);
              await editor.reloadTab(path);
            },
          });
        } else if (result.status === "error") {
          setError(result.message);
        }
      } finally {
        setSaving(false);
      }
    },
    [editor],
  );

  // --- Tab close with dirty guard ----------------------------------------
  const handleCloseTab = useCallback(
    (path: string) => {
      if (!editor.isTabDirty(path)) {
        editor.closeTab(path);
        return;
      }
      setConfirmState({
        title: "Unsaved changes",
        body: `${basename(path)} has unsaved changes. Save before closing, or discard?`,
        confirmLabel: "Save",
        onConfirm: async () => {
          setConfirmState(null);
          await doSave(path);
          if (!editor.isTabDirty(path)) editor.closeTab(path);
        },
        thirdAction: {
          label: "Discard",
          onClick: () => {
            setConfirmState(null);
            editor.closeTab(path);
          },
        },
      });
    },
    [editor, doSave],
  );

  // --- Tree CRUD actions --------------------------------------------------
  const refreshParent = useCallback(
    async (p: string) => {
      await tree.refreshPath(p);
    },
    [tree],
  );

  const startNew = useCallback((parent: string, mode: "file" | "dir") => {
    tree.ensureExpanded(parent);
    setPendingNew({ parent, mode });
  }, [tree]);

  const submitNew = useCallback(
    async (parent: string, mode: "file" | "dir", name: string) => {
      setPendingNew(null);
      setError(null);
      const target = joinChild(parent, name);
      try {
        if (mode === "dir") {
          await createDir(sessionId, target);
        } else {
          await saveFile(sessionId, target, { content: "", encoding: "utf8" });
        }
        await refreshParent(parent);
      } catch (err) {
        const msg = err instanceof WorkspaceApiError ? err.message : (err as Error).message;
        setError(msg);
      }
    },
    [sessionId, refreshParent],
  );

  const submitRename = useCallback(
    async (path: string, newName: string) => {
      setRenamingPath(null);
      setError(null);
      const parent = parentOf(path);
      const target = joinChild(parent, newName);
      if (target === path) return;
      try {
        await moveEntry(sessionId, path, target);
        editor.renameTab(path, target);
        await refreshParent(parent);
      } catch (err) {
        const msg = err instanceof WorkspaceApiError ? err.message : (err as Error).message;
        setError(msg);
      }
    },
    [sessionId, editor, refreshParent],
  );

  const confirmDelete = useCallback(
    (path: string, entry: TreeEntry) => {
      const isDir = entry.type === "dir";
      setConfirmState({
        title: isDir ? "Delete folder" : "Delete file",
        body: isDir
          ? `Delete ${entry.name} and all its contents? This cannot be undone.`
          : `Delete ${entry.name}? This cannot be undone.`,
        confirmLabel: "Delete",
        danger: true,
        onConfirm: async () => {
          setConfirmState(null);
          setError(null);
          try {
            await deleteEntry(sessionId, path, isDir);
            // Close any tab at or under this path
            for (const tab of editor.tabs) {
              if (tab.path === path || tab.path.startsWith(path + "/")) {
                editor.removeTab(tab.path);
              }
            }
            await refreshParent(parentOf(path));
          } catch (err) {
            const msg = err instanceof WorkspaceApiError ? err.message : (err as Error).message;
            setError(msg);
          }
        },
      });
    },
    [sessionId, editor, refreshParent],
  );

  const uploadFiles = useCallback(
    async (parent: string, files: FileList) => {
      setError(null);
      for (const file of Array.from(files)) {
        if (file.size > MAX_UPLOAD_BYTES) {
          setError(`${file.name} exceeds ${MAX_UPLOAD_BYTES / (1024 * 1024)} MB limit`);
          continue;
        }
        const target = joinChild(parent, file.name);
        try {
          await uploadFile(sessionId, target, file, false);
        } catch (err) {
          if (err instanceof WorkspaceApiError && err.status === 409 && err.code === "exists") {
            const proceed = window.confirm(`${file.name} already exists. Overwrite?`);
            if (proceed) {
              try {
                await uploadFile(sessionId, target, file, true);
              } catch (err2) {
                setError(err2 instanceof Error ? err2.message : "Upload failed");
              }
            }
          } else {
            setError(err instanceof Error ? err.message : "Upload failed");
          }
        }
      }
      await refreshParent(parent);
    },
    [sessionId, refreshParent],
  );

  // Create a fresh <input> per invocation rather than relying on a single
  // hidden input + ref. Avoids a class of issues where the ref click is
  // swallowed by React re-renders or the hidden input's layout state.
  const triggerUpload = useCallback(
    (parent: string) => {
      const input = document.createElement("input");
      input.type = "file";
      input.multiple = true;
      input.style.position = "fixed";
      input.style.left = "-9999px";
      input.style.top = "-9999px";
      input.style.opacity = "0";
      document.body.appendChild(input);
      input.addEventListener("change", () => {
        const files = input.files;
        document.body.removeChild(input);
        if (files && files.length > 0) {
          void uploadFiles(parent, files);
        }
      });
      input.addEventListener("cancel", () => {
        document.body.removeChild(input);
      });
      input.click();
    },
    [uploadFiles],
  );

  const triggerDownload = useCallback(
    (path: string) => {
      const url = downloadFileUrl(sessionId, path);
      const anchor = document.createElement("a");
      anchor.href = url;
      anchor.download = basename(path);
      document.body.appendChild(anchor);
      anchor.click();
      document.body.removeChild(anchor);
    },
    [sessionId],
  );

  // --- Context menu builder -----------------------------------------------
  const handleContextMenu = useCallback(
    (e: React.MouseEvent, payload: { path: string; entry: TreeEntry | null }) => {
      const { path, entry } = payload;
      const isRoot = path === "/";
      const isDir = entry === null || entry.type === "dir";
      const items: ContextMenuItem[] = [];

      if (!isDir && entry) {
        items.push({
          id: "open",
          label: "Open",
          icon: "open",
          onSelect: () => void editor.openFile(path),
        });
      }
      if (isDir) {
        items.push(
          { id: "new-file", label: "New File", icon: "new-file", onSelect: () => startNew(path, "file") },
          { id: "new-folder", label: "New Folder", icon: "new-folder", onSelect: () => startNew(path, "dir") },
          { id: "upload", label: "Upload File…", icon: "upload", onSelect: () => triggerUpload(path) },
        );
      }
      if (!isDir && entry) {
        items.push({ id: "download", label: "Download", icon: "download", onSelect: () => triggerDownload(path) });
      }
      if (!isRoot && entry) {
        items.push({
          id: "rename",
          label: "Rename",
          icon: "rename",
          onSelect: () => setRenamingPath(path),
        });
        items.push({
          id: "delete",
          label: "Delete",
          icon: "delete",
          danger: true,
          onSelect: () => confirmDelete(path, entry),
        });
      }
      setContextMenu({ x: e.clientX, y: e.clientY, items });
    },
    [editor, startNew, triggerUpload, triggerDownload, confirmDelete],
  );

  const ui: TreeInteractions = {
    activeFilePath: editor.activeTabPath,
    onFileClick: (p) => void editor.openFile(p),
    onContextMenu: handleContextMenu,
    renamingPath,
    onSubmitRename: (p, v) => void submitRename(p, v),
    onCancelRename: () => setRenamingPath(null),
    pendingNew,
    onSubmitNew: (parent, mode, name) => void submitNew(parent, mode, name),
    onCancelNew: () => setPendingNew(null),
  };

  return (
    <aside
      className="relative flex h-full shrink-0 bg-base"
      style={{ width: panelWidth }}
    >
      <ResizeHandle onMouseDown={onMouseDown} active={isResizing} />

      <div
        className={cn(
          "flex h-full flex-col border-l border-white/[0.06]",
          editorOpen ? "border-r border-white/[0.06]" : "flex-1",
        )}
        style={editorOpen ? { width: TREE_WIDTH_WITH_EDITOR } : undefined}
      >
        <WorkspaceToolbar
          showHidden={tree.showHidden}
          refreshing={refreshing}
          onRefresh={() => void refresh()}
          onToggleHidden={tree.toggleHidden}
          onCollapseAll={tree.collapseAll}
          onClosePanel={onClose}
        />
        <FileTree tree={tree} ui={ui} />
      </div>

      {editorOpen && (
        <div className="flex h-full flex-1 min-w-0 flex-col">
          <EditorTabs
            tabs={editor.tabs}
            activeTabPath={editor.activeTabPath}
            onActivate={editor.activate}
            onClose={handleCloseTab}
          />
          {error && (
            <div className="shrink-0 border-b border-danger/30 bg-danger/10 px-3 py-1 type-caption text-danger">
              {error}
              <button
                type="button"
                onClick={() => setError(null)}
                className="ml-2 text-danger/80 hover:text-danger underline"
              >
                dismiss
              </button>
            </div>
          )}
          {editor.activeTab && (
            <FileEditor
              tab={editor.activeTab}
              onDraftChange={editor.setDraft}
              onSave={(p) => void doSave(p)}
              saving={saving}
            />
          )}
        </div>
      )}

      {contextMenu && (
        <WorkspaceContextMenu
          x={contextMenu.x}
          y={contextMenu.y}
          items={contextMenu.items}
          onClose={() => setContextMenu(null)}
        />
      )}

      {conflict && (
        <ConflictDialog
          path={conflict.path}
          code={conflict.code}
          onOverwrite={() => void conflict.retryOverwrite()}
          onReload={() => void conflict.onReload()}
          onCancel={() => setConflict(null)}
        />
      )}

      {confirmState && (
        <ConfirmDialog
          title={confirmState.title}
          body={confirmState.body}
          confirmLabel={confirmState.confirmLabel}
          danger={confirmState.danger}
          thirdAction={confirmState.thirdAction}
          onConfirm={confirmState.onConfirm}
          onCancel={() => setConfirmState(null)}
        />
      )}
    </aside>
  );
}

interface ResizeHandleProps {
  onMouseDown: (e: React.MouseEvent) => void;
  active: boolean;
}

function ResizeHandle({ onMouseDown, active }: ResizeHandleProps) {
  return (
    <div
      role="separator"
      aria-orientation="vertical"
      aria-label="Resize workspace panel"
      onMouseDown={onMouseDown}
      className={cn(
        "group absolute top-0 bottom-0 z-20 w-2 -translate-x-1/2 cursor-col-resize",
        active && "select-none",
      )}
      style={{ left: 0 }}
    >
      <div
        className={cn(
          "pointer-events-none absolute top-0 bottom-0 left-1/2 -translate-x-1/2 w-px transition-colors",
          active
            ? "bg-info/80 w-0.5"
            : "bg-white/[0.08] group-hover:bg-info/60 group-hover:w-0.5",
        )}
      />
    </div>
  );
}
