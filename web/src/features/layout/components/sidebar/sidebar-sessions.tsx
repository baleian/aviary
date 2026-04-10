"use client";

import { useSidebar } from "@/features/layout/providers/sidebar-provider";
import { Skeleton } from "@/components/ui/skeleton";
import { Badge } from "@/components/ui/badge";
import { SidebarAgentGroup } from "./sidebar-agent-group";

/**
 * SidebarSessions — the section of the sidebar that lists active sessions
 * grouped by their agent. Hidden when the sidebar is collapsed.
 */
export function SidebarSessions() {
  const { groups, loading, collapsed } = useSidebar();

  if (collapsed) return null;

  const groupsWithSessions = groups.filter((g) => g.sessions.length > 0);
  const totalSessions = groups.reduce((sum, g) => sum + g.sessions.length, 0);

  return (
    <div className="px-3 pt-2">
      <div className="mb-2 flex items-center justify-between px-3">
        <span className="type-small text-fg-disabled">
          Active Sessions
          {totalSessions > 0 && (
            <Badge variant="info" className="ml-2 px-1.5">
              {totalSessions}
            </Badge>
          )}
        </span>
      </div>

      {loading ? (
        <div className="space-y-2 px-3">
          {[...Array(3)].map((_, i) => (
            <Skeleton key={i} className="h-6" />
          ))}
        </div>
      ) : groupsWithSessions.length === 0 ? (
        <p className="px-3 type-caption text-fg-disabled">No active sessions</p>
      ) : (
        <div className="space-y-1">
          {groupsWithSessions.map(({ agent, sessions }) => (
            <SidebarAgentGroup key={agent.id} agent={agent} sessions={sessions} />
          ))}
        </div>
      )}
    </div>
  );
}
