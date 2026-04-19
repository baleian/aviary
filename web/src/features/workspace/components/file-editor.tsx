"use client";

import dynamic from "next/dynamic";
import { useEffect, useMemo, useRef, useState } from "react";
import { AlertTriangle, Code2, Eye, Loader2, Save } from "@/components/icons";
import { cn } from "@/lib/utils";
import type { EditorTab } from "../hooks/use-workspace-editor";
import { monacoLanguageFor } from "../lib/file-icons";
import { MarkdownContent } from "@/features/chat/components/markdown/markdown-content";

// Monaco is a large client-only bundle; Next.js SSR explicitly disabled.
const MonacoEditor = dynamic(
  () => import("@monaco-editor/react").then((m) => m.default),
  { ssr: false, loading: () => <EditorLoading /> },
);

interface FileEditorProps {
  tab: EditorTab;
  onDraftChange: (path: string, value: string) => void;
  onSave: (path: string) => void | Promise<void>;
  saving: boolean;
}

function isMarkdownPath(p: string): boolean {
  const ext = p.slice(p.lastIndexOf(".") + 1).toLowerCase();
  return ext === "md" || ext === "markdown";
}

export function FileEditor({ tab, onDraftChange, onSave, saving }: FileEditorProps) {
  const { path, loading, error, savedContent, draft, isBinary, size } = tab;
  const dirty = draft !== null;
  const value = draft ?? savedContent ?? "";

  const language = useMemo(() => {
    const base = path.slice(path.lastIndexOf("/") + 1);
    return monacoLanguageFor(base);
  }, [path]);
  const isMarkdown = useMemo(() => isMarkdownPath(path), [path]);

  const [preview, setPreview] = useState(false);
  useEffect(() => {
    setPreview(isMarkdown && !dirty);
  }, [path, isMarkdown, dirty]);

  // Latest save callback captured for the monaco keybinding, which registers
  // once per editor instance.
  const onSaveRef = useRef(onSave);
  useEffect(() => {
    onSaveRef.current = onSave;
  }, [onSave]);

  const canEdit = !loading && !error && !isBinary;

  return (
    <div className="flex h-full flex-1 flex-col min-w-0 border-l border-white/[0.06] bg-canvas">
      <header className="flex shrink-0 items-center justify-between gap-2 border-b border-white/[0.06] px-3 py-1.5">
        <span className="truncate type-caption text-fg-muted font-mono" title={path}>
          {path}
          {dirty && <span className="ml-2 text-warning">● modified</span>}
        </span>
        <div className="flex shrink-0 items-center gap-1">
          {canEdit && (
            <button
              type="button"
              onClick={() => void onSave(path)}
              disabled={!dirty || saving}
              title={dirty ? "Save (Ctrl+S)" : "No changes to save"}
              aria-label="Save file"
              className={cn(
                "flex h-6 items-center gap-1 rounded-xs px-2 type-caption transition-colors",
                dirty && !saving
                  ? "bg-info/20 text-info hover:bg-info/30"
                  : "text-fg-muted opacity-40 cursor-not-allowed",
              )}
            >
              {saving ? (
                <Loader2 size={12} className="animate-spin" strokeWidth={2} />
              ) : (
                <Save size={12} strokeWidth={2} />
              )}
              <span>Save</span>
            </button>
          )}
          {isMarkdown && (
            <button
              type="button"
              onClick={() => setPreview((v) => !v)}
              title={preview ? "View source" : "View preview"}
              aria-label={preview ? "View markdown source" : "View markdown preview"}
              aria-pressed={preview}
              className={cn(
                "flex h-6 w-6 items-center justify-center rounded-xs transition-colors",
                preview
                  ? "bg-raised text-fg-primary"
                  : "text-fg-muted hover:bg-raised hover:text-fg-primary",
              )}
            >
              {preview ? <Code2 size={12} strokeWidth={2} /> : <Eye size={12} strokeWidth={2} />}
            </button>
          )}
        </div>
      </header>

      <div className="flex-1 min-h-0">
        {loading ? (
          <EditorLoading />
        ) : error ? (
          <div className="flex h-full items-center justify-center px-4">
            <div className="flex items-center gap-2 type-caption text-danger/80">
              <AlertTriangle size={14} strokeWidth={2} />
              {error}
            </div>
          </div>
        ) : isBinary ? (
          <div className="flex h-full items-center justify-center px-4 type-caption text-fg-muted text-center">
            Binary file — {formatSize(size)}. Preview not available.
          </div>
        ) : isMarkdown && preview ? (
          <div className="prose prose-invert h-full overflow-auto px-6 py-4 max-w-none">
            <MarkdownContent content={value} />
          </div>
        ) : (
          <MonacoEditor
            key={path}
            height="100%"
            theme="vs-dark"
            language={language}
            value={value}
            onChange={(next: string | undefined) => onDraftChange(path, next ?? "")}
            onMount={(
              editorInstance: { addCommand: (keybinding: number, handler: () => void) => void },
              monaco: { KeyMod: { CtrlCmd: number }; KeyCode: { KeyS: number } },
            ) => {
              editorInstance.addCommand(
                monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS,
                () => {
                  void onSaveRef.current(path);
                },
              );
            }}
            options={{
              readOnly: !canEdit,
              minimap: { enabled: false },
              fontSize: 13,
              wordWrap: "on",
              renderWhitespace: "selection",
              scrollBeyondLastLine: false,
              lineNumbersMinChars: 3,
              folding: true,
              automaticLayout: true,
            }}
          />
        )}
      </div>
    </div>
  );
}

function EditorLoading() {
  return (
    <div className="flex h-full items-center justify-center gap-2 type-caption text-fg-muted">
      <Loader2 size={14} className="animate-spin" strokeWidth={2} />
      Loading…
    </div>
  );
}

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}
