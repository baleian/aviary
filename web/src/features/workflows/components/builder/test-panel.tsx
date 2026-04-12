"use client";

import { useEffect, useRef, useState, useCallback } from "react";
import { Bot, Play, Check, X, CircleDot, Loader2, Globe, GitBranch, Layers, Filter, FileText } from "@/components/icons";
import { ChatInput } from "@/features/chat/components/input/chat-input";
import { TextBlockView } from "@/features/chat/components/blocks/text-block";
import { ThinkingChip } from "@/features/chat/components/blocks/thinking-chip";
import { ToolCallCard } from "@/features/chat/components/blocks/tool-call-card";
import { useWorkflowBuilder } from "@/features/workflows/providers/workflow-builder-provider";
import { useWorkflowRun, type NodeLogEntry, type RunStatus, type NodeRunStatus } from "@/features/workflows/hooks/use-workflow-run";
import { cn } from "@/lib/utils";
import type { StreamBlock, ToolCallBlock } from "@/types";

// --- Node icon mapping ---
const NODE_ICONS: Record<string, React.ReactNode> = {
  manual_trigger: <Play size={14} strokeWidth={2} />,
  webhook_trigger: <Globe size={14} strokeWidth={1.75} />,
  agent_step: <Bot size={14} strokeWidth={1.75} />,
  condition: <GitBranch size={14} strokeWidth={1.75} />,
  merge: <Layers size={14} strokeWidth={1.75} />,
  payload_parser: <Filter size={14} strokeWidth={1.75} />,
  template: <FileText size={14} strokeWidth={1.75} />,
};

const STATUS_CONFIG: Record<string, { icon: React.ReactNode; color: string }> = {
  running: { icon: <Loader2 size={12} className="animate-spin" />, color: "text-info" },
  completed: { icon: <Check size={12} strokeWidth={3} />, color: "text-success" },
  failed: { icon: <X size={12} strokeWidth={3} />, color: "text-danger" },
  skipped: { icon: <CircleDot size={12} />, color: "text-fg-disabled" },
};

// --- Node execution card ---
interface NodeCardProps {
  nodeId: string;
  nodeType: string;
  nodeLabel: string;
  status: NodeRunStatus;
  logs: NodeLogEntry[];
  isLast: boolean;
}

function NodeCard({ nodeId, nodeType, nodeLabel, status, logs, isLast }: NodeCardProps) {
  const statusCfg = STATUS_CONFIG[status];
  const isAgentStep = nodeType === "agent_step";

  // Build streaming blocks from agent step logs
  const blocks: StreamBlock[] = [];
  let thinkingContent = "";
  let textContent = "";

  if (isAgentStep) {
    for (const log of logs) {
      if (log.log_type === "thinking") {
        thinkingContent += log.content ?? "";
      } else if (log.log_type === "chunk") {
        if (thinkingContent) {
          blocks.push({ type: "thinking", id: `t-${blocks.length}`, content: thinkingContent });
          thinkingContent = "";
        }
        textContent += log.content ?? "";
      } else if (log.log_type === "tool_use") {
        if (thinkingContent) {
          blocks.push({ type: "thinking", id: `t-${blocks.length}`, content: thinkingContent });
          thinkingContent = "";
        }
        if (textContent) {
          blocks.push({ type: "text", id: `x-${blocks.length}`, content: textContent });
          textContent = "";
        }
        blocks.push({
          type: "tool_call",
          id: `tc-${blocks.length}`,
          name: log.name ?? "unknown",
          input: (log.input ?? {}) as Record<string, unknown>,
          status: "running",
        } as ToolCallBlock);
      } else if (log.log_type === "tool_result") {
        const lastTool = [...blocks].reverse().find((b) => b.type === "tool_call") as ToolCallBlock | undefined;
        if (lastTool) {
          lastTool.status = "complete";
          lastTool.result = log.content;
        }
      }
    }
    if (thinkingContent) blocks.push({ type: "thinking", id: `t-${blocks.length}`, content: thinkingContent });
    if (textContent) blocks.push({ type: "text", id: `x-${blocks.length}`, content: textContent });
  }

  // Non-agent nodes: show last output log
  const outputLog = !isAgentStep ? logs.find((l) => l.log_type === "chunk") : null;

  return (
    <div className={cn("rounded-lg border border-white/[0.06] bg-[rgb(16_17_17)]", isLast && status === "running" && "border-info/20")}>
      {/* Card header */}
      <div className="flex items-center gap-2 px-3 py-2 border-b border-white/[0.06]">
        <span className="text-fg-muted">{NODE_ICONS[nodeType] ?? <CircleDot size={14} />}</span>
        <span className="text-[12px] font-medium text-fg-primary flex-1 truncate">{nodeLabel}</span>
        {statusCfg && (
          <span className={cn("flex items-center gap-1 text-[11px]", statusCfg.color)}>
            {statusCfg.icon}
          </span>
        )}
      </div>

      {/* Card body */}
      <div className="px-3 py-2">
        {isAgentStep && blocks.length > 0 && (
          <div className="space-y-2">
            {blocks.map((block) => {
              if (block.type === "thinking") {
                return <ThinkingChip key={block.id} content={block.content} isActive={status === "running"} />;
              }
              if (block.type === "text") {
                return <TextBlockView key={block.id} content={block.content} />;
              }
              if (block.type === "tool_call") {
                return <ToolCallCard key={block.id} block={block as ToolCallBlock} />;
              }
              return null;
            })}
          </div>
        )}

        {isAgentStep && blocks.length === 0 && status === "running" && (
          <p className="text-[11px] text-fg-disabled animate-pulse">Processing…</p>
        )}

        {!isAgentStep && outputLog && (
          <p className="text-[12px] text-fg-secondary break-words">{outputLog.content}</p>
        )}

        {!isAgentStep && !outputLog && status === "completed" && (
          <p className="text-[11px] text-fg-disabled">Done</p>
        )}

        {status === "skipped" && (
          <p className="text-[11px] text-fg-disabled">Skipped</p>
        )}

        {status === "failed" && (
          <p className="text-[11px] text-danger">
            {logs.find((l) => l.log_type === "error")?.content ?? "Failed"}
          </p>
        )}
      </div>
    </div>
  );
}

// --- Test Panel ---
interface TestPanelProps {
  run: ReturnType<typeof useWorkflowRun>;
}

export function TestPanel({ run }: TestPanelProps) {
  const { nodes: graphNodes, workflowStatus } = useWorkflowBuilder();
  const scrollRef = useRef<HTMLDivElement>(null);
  const [userInput, setUserInput] = useState<string | null>(null);

  // Find trigger type from graph
  const triggerNode = graphNodes.find((n) => n.type === "manual_trigger" || n.type === "webhook_trigger");
  const triggerType = triggerNode?.type ?? "manual_trigger";
  const isWebhook = triggerType === "webhook_trigger";

  // Auto-scroll
  useEffect(() => {
    if (scrollRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
    }
  }, [run.logs, run.nodeStatuses]);

  const handleSend = useCallback(
    (content: string) => {
      setUserInput(content);
      const triggerData = isWebhook ? (() => { try { return JSON.parse(content); } catch { return { raw: content }; } })() : { text: content };
      run.trigger(triggerData);
    },
    [run, isWebhook],
  );

  // Build ordered node cards from statuses + logs
  const nodeCards: { nodeId: string; nodeType: string; label: string; status: NodeRunStatus; logs: NodeLogEntry[] }[] = [];
  const executionOrder = Object.keys(run.nodeStatuses);

  for (const nodeId of executionOrder) {
    const graphNode = graphNodes.find((n) => n.id === nodeId);
    const nodeType = graphNode?.type ?? "unknown";
    const label = (graphNode?.data as Record<string, unknown>)?.label as string ?? nodeId;
    const status = run.nodeStatuses[nodeId];
    const logs = run.logs.filter((l) => l.node_id === nodeId);
    nodeCards.push({ nodeId, nodeType, label, status, logs });
  }

  return (
    <div className="flex flex-1 flex-col bg-[rgb(10_11_13)] w-72 min-w-[240px]">
      {/* Messages area */}
      <div ref={scrollRef} className="flex-1 overflow-y-auto px-3 py-3 space-y-3">
        {run.runStatus === "idle" && !userInput && (
          <p className="text-center text-[12px] text-fg-disabled py-8">
            {isWebhook ? "Enter a JSON payload to test" : "Send a message to test your workflow"}
          </p>
        )}

        {/* User input bubble */}
        {userInput && (
          <div className="flex justify-end">
            <div className="max-w-[85%] rounded-xl rounded-tr-sm bg-brand/10 px-3 py-2">
              <p className="text-[12px] text-fg-primary whitespace-pre-wrap break-words">{userInput}</p>
            </div>
          </div>
        )}

        {/* Node execution cards */}
        {nodeCards.map((card, i) => (
          <NodeCard
            key={card.nodeId}
            nodeId={card.nodeId}
            nodeType={card.nodeType}
            nodeLabel={card.label}
            status={card.status}
            logs={card.logs}
            isLast={i === nodeCards.length - 1}
          />
        ))}

        {/* Run error */}
        {run.error && (
          <div className="rounded-lg border border-danger/20 bg-danger/[0.04] px-3 py-2">
            <p className="text-[11px] text-danger">{run.error}</p>
          </div>
        )}

        {/* Run complete */}
        {run.runStatus === "completed" && (
          <p className="text-center text-[11px] text-success py-1">Workflow completed</p>
        )}
      </div>

      {/* Input */}
      <div className="border-t border-white/[0.06] px-2 py-2">
        <ChatInput
          onSend={handleSend}
          disabled={run.isRunning}
          isStreaming={run.isRunning}
          onCancel={run.cancel}
          placeholder={isWebhook ? '{"key": "value"}' : "Type a message…"}
        />
      </div>
    </div>
  );
}
