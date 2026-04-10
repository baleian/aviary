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
 */
export function SidebarSessionItem({
  session,
  isActive,
}: {
  session: Session;
  isActive: boolean;
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
