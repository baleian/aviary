"use client";

import Link from "next/link";
import { PanelLeftClose, PanelLeft } from "@/components/icons";
import { useSidebar } from "@/features/layout/providers/sidebar-provider";
import { routes } from "@/lib/constants/routes";

/**
 * SidebarBrand — top header of the sidebar with logo + collapse toggle.
 * Pure presentation; data comes from useSidebar.
 */
export function SidebarBrand() {
  const { collapsed, toggleCollapsed } = useSidebar();

  return (
    <div className="flex h-14 shrink-0 items-center justify-between border-b border-white/[0.06] px-4">
      {!collapsed && (
        <Link href={routes.agents} className="flex items-center gap-2.5">
          <div className="flex h-7 w-7 items-center justify-center rounded-sm bg-elevated shadow-3">
            <svg
              width="14"
              height="14"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.75"
              strokeLinecap="round"
              strokeLinejoin="round"
              className="text-brand"
              aria-hidden="true"
            >
              <path d="M12 2L2 7l10 5 10-5-10-5z" />
              <path d="M2 17l10 5 10-5" />
              <path d="M2 12l10 5 10-5" />
            </svg>
          </div>
          <span className="type-button text-fg-primary">Aviary</span>
        </Link>
      )}
      <button
        type="button"
        onClick={toggleCollapsed}
        className="flex h-7 w-7 items-center justify-center rounded-sm text-fg-muted hover:bg-raised hover:text-fg-primary transition-colors"
        title={collapsed ? "Expand sidebar" : "Collapse sidebar"}
        aria-label={collapsed ? "Expand sidebar" : "Collapse sidebar"}
      >
        {collapsed ? <PanelLeft size={14} strokeWidth={1.75} /> : <PanelLeftClose size={14} strokeWidth={1.75} />}
      </button>
    </div>
  );
}
