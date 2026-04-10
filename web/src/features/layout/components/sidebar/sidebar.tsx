"use client";

import { useSidebar } from "@/features/layout/providers/sidebar-provider";
import { SidebarBrand } from "./sidebar-brand";
import { SidebarNav } from "./sidebar-nav";
import { SidebarSessions } from "./sidebar-sessions";
import { SidebarUser } from "./sidebar-user";
import { cn } from "@/lib/utils";

/**
 * Sidebar — assembles brand, nav, sessions, and user sections.
 * All state lives in SidebarProvider; this component is purely structural.
 */
export function Sidebar() {
  const { collapsed } = useSidebar();

  return (
    <aside
      className={cn(
        "flex shrink-0 flex-col border-r border-white/[0.06] bg-elevated transition-all duration-200",
        collapsed ? "w-16" : "w-64",
      )}
    >
      <SidebarBrand />

      <div className="flex-1 overflow-y-auto py-3">
        <SidebarNav />
        <SidebarSessions />
      </div>

      <SidebarUser />
    </aside>
  );
}
