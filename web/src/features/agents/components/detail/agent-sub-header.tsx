"use client";

import Link from "next/link";
import { Pencil, Wrench, PanelRight, PanelRightClose } from "@/components/icons";
import { Avatar } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import { toneFromId, initialFromName } from "@/lib/tone";
import { routes } from "@/lib/constants/routes";
import { cn } from "@/lib/utils";
import type { Agent } from "@/types";

export interface AgentSubHeaderProps {
  agent: Agent;
  /** Optional workspace rail toggle — only shown when handler provided. */
  workspaceOpen?: boolean;
  onToggleWorkspace?: () => void;
}

/**
 * One-line agent identity row that sits directly under the AppShell
 * breadcrumb. Holds the visual identity (tone avatar, description,
 * model/tools chips) plus the Edit shortcut so the chat pane below
 * stays full height.
 */
export function AgentSubHeader({
  agent,
  workspaceOpen,
  onToggleWorkspace,
}: AgentSubHeaderProps) {
  const tone = toneFromId(agent.id);
  const toolCount = (agent.tools?.length ?? 0) + (agent.mcp_servers?.length ?? 0);
  const model = agent.model_config?.model ?? agent.model_config?.backend ?? "—";
  return (
    <div
      className={cn(
        "flex items-center gap-3 border-b border-border-subtle bg-canvas",
        "h-[44px] px-4"
      )}
    >
      <Link
        href={routes.agentDetail(agent.id)}
        className={cn(
          "group flex min-w-0 items-center gap-2",
          "rounded-[7px] px-1 -mx-1 py-0.5 -my-0.5",
          "transition-colors duration-fast hover:bg-hover"
        )}
        title="Open agent detail"
      >
        <Avatar tone={tone} size="md">
          {agent.icon || initialFromName(agent.name)}
        </Avatar>
        <span className="t-h3 fg-primary truncate group-hover:underline decoration-fg-muted underline-offset-2">
          {agent.name}
        </span>
      </Link>
      {agent.description?.trim() && (
        <span className="hidden min-w-0 flex-1 truncate text-[12px] text-fg-tertiary sm:inline">
          {agent.description}
        </span>
      )}
      {!agent.description?.trim() && <div className="flex-1" />}
      <Chip>
        <span className="t-mono">{model}</span>
      </Chip>
      <Chip>
        <Wrench size={11} />
        <span className="num t-mono">{toolCount}</span>
        <span className="text-fg-muted">tools</span>
      </Chip>
      <Button asChild variant="outline" size="sm">
        <Link href={routes.agentEdit(agent.id)}>
          <Pencil size={12} /> Edit
        </Link>
      </Button>
      {onToggleWorkspace && (
        <button
          type="button"
          onClick={onToggleWorkspace}
          className={cn(
            "inline-flex h-7 w-7 items-center justify-center rounded-[6px]",
            "text-fg-secondary transition-colors duration-fast",
            workspaceOpen ? "bg-hover text-fg-primary" : "hover:bg-hover hover:text-fg-primary"
          )}
          aria-label={workspaceOpen ? "Hide workspace" : "Show workspace"}
          aria-pressed={workspaceOpen}
          title={workspaceOpen ? "Hide workspace" : "Show workspace"}
        >
          {workspaceOpen ? <PanelRightClose size={14} /> : <PanelRight size={14} />}
        </button>
      )}
    </div>
  );
}

function Chip({ children }: { children: React.ReactNode }) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1 h-[22px] px-2 rounded-[5px]",
        "bg-hover text-[11.5px] text-fg-secondary"
      )}
    >
      {children}
    </span>
  );
}
