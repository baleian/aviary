/**
 * Example AppShell — this is what replaces the authenticated layout.
 * Thin wrapper; the heavy lifting is in Sidebar + Header + modals.
 *
 * Target location: src/features/layout/components/app-shell.tsx
 */
"use client";

import * as React from "react";
import { useRoute } from "../providers/route-provider";
import { Sidebar } from "./sidebar";
import { Header } from "./header";
import { CommandPalette } from "@/features/search/components/command-palette";
import { NotificationsPanel } from "./notifications-panel";
import { UserMenu } from "./user-menu";

import { Dashboard } from "@/features/dashboard/components/dashboard";
import { AgentsList } from "@/features/agents/components/agents-list";
import { AgentDetail } from "@/features/agents/components/agent-detail";
import { WorkflowsList } from "@/features/workflows/components/workflows-list";
import { WorkflowDetail } from "@/features/workflows/components/workflow-detail";
import { MarketplaceList } from "@/features/marketplace/components/marketplace-list";
import { MarketplaceItem } from "@/features/marketplace/components/marketplace-item";

export function AppShell() {
  const { route } = useRoute();
  const [searchOpen, setSearchOpen] = React.useState(false);
  const [notifOpen, setNotifOpen] = React.useState(false);
  const [userMenuOpen, setUserMenuOpen] = React.useState(false);
  const [sidebarCollapsed, setSidebarCollapsed] = React.useState(false);

  // Global ⌘K binding
  React.useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === "k") {
        e.preventDefault();
        setSearchOpen((v) => !v);
      }
    };
    window.addEventListener("keydown", handler);
    return () => window.removeEventListener("keydown", handler);
  }, []);

  return (
    <div className="flex h-screen overflow-hidden bg-canvas text-fg-primary">
      <Sidebar
        collapsed={sidebarCollapsed}
        onToggleCollapse={() => setSidebarCollapsed((v) => !v)}
      />
      <div className="flex-1 flex flex-col min-w-0">
        <Header
          onOpenSearch={() => setSearchOpen(true)}
          onOpenNotifications={() => setNotifOpen((v) => !v)}
          onOpenUserMenu={() => setUserMenuOpen((v) => !v)}
          notifOpen={notifOpen}
          userMenuOpen={userMenuOpen}
        />
        <main className="flex-1 overflow-y-auto">
          <RouteContent />
        </main>
      </div>

      <CommandPalette open={searchOpen} onClose={() => setSearchOpen(false)} />
      <NotificationsPanel open={notifOpen} onClose={() => setNotifOpen(false)} />
      <UserMenu open={userMenuOpen} onClose={() => setUserMenuOpen(false)} />
    </div>
  );
}

function RouteContent() {
  const { route } = useRoute();
  switch (route.primary) {
    case "dashboard":         return <Dashboard />;
    case "agents":            return <AgentsList />;
    case "agent":             return <AgentDetail />;
    case "workflows":         return <WorkflowsList />;
    case "workflow":          return <WorkflowDetail />;
    case "marketplace":       return <MarketplaceList />;
    case "marketplace-item":  return <MarketplaceItem />;
    default:                  return <Dashboard />;
  }
}
