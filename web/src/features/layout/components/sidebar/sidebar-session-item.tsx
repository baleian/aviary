"use client";

import { useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { Trash2, Check } from "@/components/icons";
import { Spinner } from "@/components/ui/spinner";
import { useSidebar } from "@/features/layout/providers/sidebar-provider";
import { useSessionStatus } from "@/features/layout/providers/session-status-provider";
import { routes } from "@/lib/constants/routes";
import { cn } from "@/lib/utils";
import type { Session } from "@/types";

/**
 * SidebarSessionItem — single session row in the sidebar.
 *
 * Two-step delete: first click marks "confirming", second confirms.
 * The confirming state resets on mouse leave so accidents are easy to undo.
 *
 * In "by date" view, the parent agent group is not shown, so each row
 * receives an optional `agentIcon` prefix (with `agentName` as tooltip)
 * to identify which agent the session belongs to. In "by agent" view
 * the prefix is omitted (the parent group header carries that context).
 */
export function SidebarSessionItem({
  session,
  isActive,
  agentIcon,
  agentName,
}: {
  session: Session;
  isActive: boolean;
  /** Optional agent icon prefix shown in date-grouped view */
  agentIcon?: string;
  /** Tooltip for the icon prefix */
  agentName?: string;
}) {
  const { status, unread, title: polledTitle } = useSessionStatus(session.id);
  const { deleteSession } = useSidebar();
  const router = useRouter();
  const [confirming, setConfirming] = useState(false);
  const [deleting, setDeleting] = useState(false);

  const handleDelete = async (e: React.MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
    if (!confirming) {
      setConfirming(true);
      return;
    }
    setDeleting(true);
    try {
      await deleteSession(session.id);
      if (isActive) router.push(routes.agents);
    } catch {
      setDeleting(false);
      setConfirming(false);
    }
  };

  return (
    <Link
      href={routes.session(session.id)}
      className={cn(
        "group flex items-center gap-1.5 rounded-xs px-2 py-1.5 type-caption transition-colors",
        isActive
          ? "bg-info/10 text-info"
          : "text-fg-muted hover:bg-white/[0.03] hover:text-fg-primary",
        deleting && "opacity-50 pointer-events-none",
      )}
      onMouseLeave={() => setConfirming(false)}
    >
      {agentIcon && (
        <span
          className="shrink-0 text-[11px] leading-none"
          title={agentName || undefined}
          aria-label={agentName || undefined}
        >
          {agentIcon}
        </span>
      )}
      {confirming ? (
        <span className="truncate flex-1 text-danger">Delete?</span>
      ) : (
        <span className="truncate flex-1">
          {polledTitle || session.title || "Untitled"}
        </span>
      )}

      {status === "streaming" && !confirming && (
        <Spinner size={12} className="text-info shrink-0" />
      )}

      {unread > 0 && !isActive && status !== "streaming" && !confirming && (
        <span className="flex h-4 min-w-4 items-center justify-center rounded-pill bg-info px-1 text-[9px] font-semibold text-canvas">
          {unread > 99 ? "99+" : unread}
        </span>
      )}

      <button
        type="button"
        onClick={handleDelete}
        className={cn(
          "flex h-4 w-4 shrink-0 items-center justify-center rounded transition-colors",
          confirming
            ? "text-danger"
            : "opacity-0 group-hover:opacity-100 text-fg-muted hover:text-danger",
        )}
        title={confirming ? "Click again to confirm" : "Delete session"}
        aria-label={confirming ? "Confirm delete" : "Delete session"}
      >
        {confirming ? <Check size={12} strokeWidth={2.5} /> : <Trash2 size={12} strokeWidth={1.75} />}
      </button>
    </Link>
  );
}
