"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { useAgentStatus } from "@/features/layout/providers/agent-status-provider";
import { SidebarSessionItem } from "./sidebar-session-item";
import { routes } from "@/lib/constants/routes";
import { cn } from "@/lib/utils";
import type { Agent, Session } from "@/types";

interface SidebarAgentGroupProps {
  agent: Agent;
  sessions: Session[];
}

/**
 * SidebarAgentGroup — header row for an agent + nested session list.
 *
 * The header shows agent icon, name, and a readiness dot. Deleted agents
 * render with strike-through and a "deleted" tag instead of the dot.
 */
export function SidebarAgentGroup({ agent, sessions }: SidebarAgentGroupProps) {
  const pathname = usePathname();
  const isActive = pathname.startsWith(`/agents/${agent.id}`);
  const readiness = useAgentStatus(agent.id);
  const isDeleted = agent.status === "deleted";

  return (
    <div>
      <Link
        href={routes.agent(agent.id)}
        className={cn(
          "flex items-center gap-2 rounded-sm px-3 py-1.5 type-caption transition-colors",
          isActive ? "text-fg-primary" : "text-fg-muted hover:text-fg-primary",
        )}
      >
        <span className={cn("text-sm", isDeleted && "grayscale opacity-40")}>
          {agent.icon || "🤖"}
        </span>
        <span
          className={cn(
            "truncate flex-1",
            isDeleted && "text-fg-disabled line-through decoration-fg-disabled decoration-1",
          )}
        >
          {agent.name}
        </span>
        {isDeleted ? (
          <span className="shrink-0 text-[9px] text-danger/60">deleted</span>
        ) : (
          <span
            className={cn(
              "h-1.5 w-1.5 shrink-0 rounded-full",
              readiness === "ready" ? "bg-success" : "bg-fg-disabled/50",
            )}
            title={readiness === "ready" ? "Agent ready" : "Agent offline"}
            aria-label={readiness === "ready" ? "Agent ready" : "Agent offline"}
          />
        )}
      </Link>

      <div className="ml-5 space-y-0.5 border-l border-white/[0.06] pl-3">
        {sessions.map((session) => (
          <SidebarSessionItem
            key={session.id}
            session={session}
            isActive={pathname === routes.session(session.id)}
          />
        ))}
      </div>
    </div>
  );
}
