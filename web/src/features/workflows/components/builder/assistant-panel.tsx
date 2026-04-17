"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import {
  Sparkles,
  Send,
  Check,
  X,
  ChevronUp,
  ChevronDown,
  Trash2,
  Loader2,
  AlertCircle,
  Plus,
  Pencil,
} from "@/components/icons";
import { useWorkflowBuilder } from "@/features/workflows/providers/workflow-builder-provider";
import { workflowsApi } from "@/features/workflows/api/workflows-api";
import {
  describePlanOp,
  type AssistantChatMessage,
  type PlanOp,
} from "@/features/workflows/lib/assistant-plan";
import type { WorkflowEdge, WorkflowNode } from "@/features/workflows/lib/types";
import { extractErrorMessage } from "@/lib/http";
import { cn } from "@/lib/utils";

// --- Helpers ---

// Deep-strip React Flow internals before sending to the LLM so we don't
// waste tokens on state the server doesn't care about.
function stripNodesEdgesForSend(nodes: WorkflowNode[], edges: WorkflowEdge[]) {
  return {
    nodes: nodes.map((n) => ({
      id: n.id,
      type: n.type,
      position: { x: n.position.x, y: n.position.y },
      data: JSON.parse(JSON.stringify(n.data)),
    })),
    edges: edges.map((e) => ({
      id: e.id,
      source: e.source,
      target: e.target,
      ...(e.sourceHandle != null && { sourceHandle: e.sourceHandle }),
      ...(e.targetHandle != null && { targetHandle: e.targetHandle }),
    })),
  };
}

function historyForApi(
  messages: AssistantChatMessage[],
): { role: "user" | "assistant"; content: string }[] {
  return messages.map((m) => ({ role: m.role, content: m.content }));
}

// --- Plan preview card ---

function PlanToneBadge({ op }: { op: PlanOp }) {
  const info = describePlanOp(op);
  const toneClasses =
    info.tone === "add"
      ? "bg-[rgba(95,201,146,0.12)] text-[rgb(95,201,146)]"
      : info.tone === "remove"
        ? "bg-[rgba(255,99,99,0.12)] text-[rgb(255,99,99)]"
        : "bg-[rgba(85,179,255,0.12)] text-[rgb(85,179,255)]";

  const Icon =
    info.tone === "add" ? Plus : info.tone === "remove" ? Trash2 : Pencil;

  return (
    <div className="flex items-start gap-2">
      <span
        className={cn(
          "mt-[1px] flex h-4 w-4 shrink-0 items-center justify-center rounded-sm",
          toneClasses,
        )}
      >
        <Icon size={10} strokeWidth={2.25} />
      </span>
      <div className="min-w-0 flex-1 text-[11.5px] leading-relaxed">
        <span className="font-medium text-fg-primary">{info.verb}</span>
        <span className="ml-1.5 text-fg-muted break-words">{info.detail}</span>
      </div>
    </div>
  );
}

function PlanPreview({
  plan,
  status,
  error,
  onAccept,
  onReject,
}: {
  plan: PlanOp[];
  status: AssistantChatMessage["planStatus"];
  error?: string;
  onAccept: () => void;
  onReject: () => void;
}) {
  return (
    <div className="mt-2 rounded-md border border-white/[0.08] bg-[rgba(255,255,255,0.02)] p-2.5">
      <div className="mb-2 flex items-center justify-between">
        <span className="text-[10px] font-semibold uppercase tracking-wider text-fg-disabled">
          Proposed changes ({plan.length})
        </span>
        {status === "accepted" && (
          <span className="flex items-center gap-1 text-[10px] text-[rgb(95,201,146)]">
            <Check size={10} strokeWidth={2.25} /> Applied
          </span>
        )}
        {status === "rejected" && (
          <span className="text-[10px] text-fg-disabled">Rejected</span>
        )}
      </div>

      <div className="space-y-1.5">
        {plan.map((op, i) => (
          <PlanToneBadge key={i} op={op} />
        ))}
      </div>

      {error && (
        <div className="mt-2 flex items-start gap-1.5 rounded-sm bg-[rgba(255,99,99,0.08)] px-2 py-1.5 text-[11px] text-[rgb(255,99,99)]">
          <AlertCircle size={11} strokeWidth={2} className="mt-[1px] shrink-0" />
          <span className="break-words">{error}</span>
        </div>
      )}

      {status === "pending" && (
        <div className="mt-2.5 flex gap-1.5">
          <button
            type="button"
            onClick={onAccept}
            className="flex-1 rounded-md bg-info px-2.5 py-1.5 text-[11px] font-medium text-canvas hover:opacity-85 transition-opacity"
          >
            Accept
          </button>
          <button
            type="button"
            onClick={onReject}
            className="flex-1 rounded-md border border-white/[0.08] bg-transparent px-2.5 py-1.5 text-[11px] font-medium text-fg-muted hover:bg-white/[0.04] transition-colors"
          >
            Reject
          </button>
        </div>
      )}
    </div>
  );
}

// --- Message bubble ---

function MessageRow({
  message,
  onAccept,
  onReject,
}: {
  message: AssistantChatMessage;
  onAccept: (id: string) => void;
  onReject: (id: string) => void;
}) {
  const isUser = message.role === "user";
  return (
    <div className={cn("flex gap-2", isUser ? "justify-end" : "justify-start")}>
      {!isUser && (
        <div className="mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-info/15 text-info">
          <Sparkles size={12} strokeWidth={2} />
        </div>
      )}
      <div
        className={cn(
          "max-w-[85%] rounded-lg px-3 py-2 text-[12.5px] leading-relaxed",
          isUser
            ? "bg-info/15 text-fg-primary"
            : "bg-[rgba(255,255,255,0.03)] text-fg-primary",
        )}
      >
        {message.content && (
          <p className="whitespace-pre-wrap break-words">{message.content}</p>
        )}
        {message.error && !message.plan && (
          <p className="mt-1 flex items-center gap-1.5 text-[11.5px] text-[rgb(255,99,99)]">
            <AlertCircle size={11} strokeWidth={2} /> {message.error}
          </p>
        )}
        {message.plan && message.plan.length > 0 && (
          <PlanPreview
            plan={message.plan}
            status={message.planStatus}
            error={message.error}
            onAccept={() => onAccept(message.id)}
            onReject={() => onReject(message.id)}
          />
        )}
      </div>
    </div>
  );
}

// --- Resize handle (horizontal, top edge) ---

function TopResizeHandle({ onResize }: { onResize: (delta: number) => void }) {
  const dragging = useRef(false);
  const lastY = useRef(0);

  const onMouseDown = useCallback(
    (e: React.MouseEvent) => {
      e.preventDefault();
      dragging.current = true;
      lastY.current = e.clientY;
      const onMove = (ev: MouseEvent) => {
        if (!dragging.current) return;
        onResize(lastY.current - ev.clientY);
        lastY.current = ev.clientY;
      };
      const onUp = () => {
        dragging.current = false;
        window.removeEventListener("mousemove", onMove);
        window.removeEventListener("mouseup", onUp);
      };
      window.addEventListener("mousemove", onMove);
      window.addEventListener("mouseup", onUp);
    },
    [onResize],
  );

  return (
    <div
      onMouseDown={onMouseDown}
      className="absolute left-0 right-0 top-0 z-10 h-1 cursor-row-resize hover:bg-info/30 active:bg-info/50 transition-colors"
    />
  );
}

// --- Main panel ---

const MIN_HEIGHT = 160;
const MAX_HEIGHT = 600;
const DEFAULT_HEIGHT = 280;
const COLLAPSED_HEIGHT = 36;

export function AssistantPanel() {
  const { workflowId, nodes, edges, applyPlan, workflowModelBackend, workflowModelName } =
    useWorkflowBuilder();

  const [messages, setMessages] = useState<AssistantChatMessage[]>([]);
  const [input, setInput] = useState("");
  const [busy, setBusy] = useState(false);
  const [height, setHeight] = useState(DEFAULT_HEIGHT);
  const [collapsed, setCollapsed] = useState(false);

  const listRef = useRef<HTMLDivElement>(null);
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  // Scroll to bottom on new messages
  useEffect(() => {
    listRef.current?.scrollTo({ top: listRef.current.scrollHeight, behavior: "smooth" });
  }, [messages, busy]);

  // Auto-resize textarea
  useEffect(() => {
    const el = textareaRef.current;
    if (!el) return;
    el.style.height = "0";
    el.style.height = `${Math.min(el.scrollHeight, 120)}px`;
  }, [input]);

  const handleResize = useCallback((delta: number) => {
    setCollapsed(false);
    setHeight((h) => Math.min(MAX_HEIGHT, Math.max(MIN_HEIGHT, h + delta)));
  }, []);

  const modelReady = Boolean(workflowModelBackend && workflowModelName);

  const send = useCallback(async () => {
    const content = input.trim();
    if (!content || busy || !modelReady) return;

    const userMsg: AssistantChatMessage = {
      id: crypto.randomUUID(),
      role: "user",
      content,
    };
    const priorHistory = historyForApi(messages);
    setMessages((prev) => [...prev, userMsg]);
    setInput("");
    setBusy(true);

    try {
      const res = await workflowsApi.assistant(workflowId, {
        user_message: content,
        current_definition: stripNodesEdgesForSend(nodes, edges),
        history: priorHistory,
      });

      const assistantMsg: AssistantChatMessage = {
        id: crypto.randomUUID(),
        role: "assistant",
        content: res.reply,
        plan: res.plan,
        planStatus: res.plan.length > 0 ? "pending" : undefined,
      };
      setMessages((prev) => [...prev, assistantMsg]);
    } catch (err) {
      const assistantMsg: AssistantChatMessage = {
        id: crypto.randomUUID(),
        role: "assistant",
        content: "",
        error: extractErrorMessage(err) || "Assistant request failed",
      };
      setMessages((prev) => [...prev, assistantMsg]);
    } finally {
      setBusy(false);
    }
  }, [input, busy, modelReady, messages, nodes, edges, workflowId]);

  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      send();
    }
  };

  const handleAccept = useCallback(
    (messageId: string) => {
      setMessages((prev) => {
        const msg = prev.find((m) => m.id === messageId);
        if (!msg?.plan) return prev;
        const result = applyPlan(msg.plan);
        return prev.map((m) =>
          m.id === messageId
            ? {
                ...m,
                planStatus: result.ok ? "accepted" : m.planStatus,
                error: result.ok ? undefined : result.error,
              }
            : m,
        );
      });
    },
    [applyPlan],
  );

  const handleReject = useCallback((messageId: string) => {
    setMessages((prev) =>
      prev.map((m) => (m.id === messageId ? { ...m, planStatus: "rejected" } : m)),
    );
  }, []);

  const handleClear = useCallback(() => {
    setMessages([]);
  }, []);

  const effectiveHeight = collapsed ? COLLAPSED_HEIGHT : height;

  return (
    <div
      className="relative flex shrink-0 flex-col border-t border-white/[0.06] bg-[rgb(10_11_13)]"
      style={{ height: effectiveHeight }}
    >
      {!collapsed && <TopResizeHandle onResize={handleResize} />}

      {/* Header */}
      <div className="flex h-9 shrink-0 items-center justify-between border-b border-white/[0.06] px-3">
        <button
          type="button"
          onClick={() => setCollapsed((c) => !c)}
          className="flex items-center gap-1.5 text-fg-muted hover:text-fg-primary transition-colors"
        >
          <Sparkles size={13} strokeWidth={2} className="text-info" />
          <span className="text-[11.5px] font-medium">AI Assistant</span>
          {collapsed ? (
            <ChevronUp size={12} strokeWidth={2} />
          ) : (
            <ChevronDown size={12} strokeWidth={2} />
          )}
        </button>

        {!collapsed && messages.length > 0 && (
          <button
            type="button"
            onClick={handleClear}
            className="flex items-center gap-1 text-[10.5px] text-fg-disabled hover:text-fg-muted transition-colors"
            title="Clear conversation"
          >
            <X size={11} strokeWidth={2} />
            Clear
          </button>
        )}
      </div>

      {!collapsed && (
        <>
          {/* Messages */}
          <div
            ref={listRef}
            className="flex-1 overflow-y-auto px-3 py-2.5 space-y-2.5"
          >
            {messages.length === 0 && (
              <div className="flex h-full flex-col items-center justify-center px-6 text-center">
                <Sparkles size={18} strokeWidth={1.75} className="mb-2 text-info/60" />
                <p className="text-[12px] text-fg-muted leading-relaxed max-w-[340px]">
                  Describe the workflow you want to build, or a change you'd like to
                  make. I'll propose the edits for you to review.
                </p>
                <p className="mt-1.5 text-[10.5px] text-fg-disabled">
                  e.g. "Add an agent step after the webhook trigger that summarizes
                  the payload."
                </p>
              </div>
            )}

            {messages.map((m) => (
              <MessageRow
                key={m.id}
                message={m}
                onAccept={handleAccept}
                onReject={handleReject}
              />
            ))}

            {busy && (
              <div className="flex items-center gap-2 pl-8 text-[11.5px] text-fg-disabled">
                <Loader2 size={12} strokeWidth={2} className="animate-spin" />
                <span>Thinking…</span>
              </div>
            )}
          </div>

          {/* Input */}
          <div className="shrink-0 border-t border-white/[0.06] px-3 py-2">
            {!modelReady && (
              <p className="mb-1.5 flex items-center gap-1 text-[10.5px] text-[rgb(255,99,99)]">
                <AlertCircle size={11} strokeWidth={2} />
                Workflow has no default model — configure it in Settings first.
              </p>
            )}
            <div className="flex items-end gap-1.5 rounded-md bg-[rgba(255,255,255,0.03)] p-1.5 focus-within:bg-[rgba(255,255,255,0.05)] transition-colors">
              <textarea
                ref={textareaRef}
                value={input}
                onChange={(e) => setInput(e.target.value)}
                onKeyDown={handleKeyDown}
                disabled={busy || !modelReady}
                placeholder={
                  modelReady
                    ? "Ask the assistant to build or change your workflow…"
                    : "Set a default model in Settings to enable the assistant"
                }
                rows={1}
                className="min-h-[28px] max-h-[120px] flex-1 resize-none bg-transparent px-2 py-1.5 text-[12.5px] text-fg-primary placeholder:text-fg-disabled focus:outline-none disabled:opacity-50"
              />
              <button
                type="button"
                onClick={send}
                disabled={busy || !input.trim() || !modelReady}
                className="flex h-7 w-7 shrink-0 items-center justify-center rounded-md bg-info text-canvas hover:opacity-85 transition-opacity disabled:opacity-30 disabled:cursor-not-allowed"
                aria-label="Send"
              >
                {busy ? (
                  <Loader2 size={13} strokeWidth={2.25} className="animate-spin" />
                ) : (
                  <Send size={13} strokeWidth={2.25} />
                )}
              </button>
            </div>
          </div>
        </>
      )}
    </div>
  );
}
