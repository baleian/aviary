"use client";

import { createContext, useCallback, useContext, useEffect, useState } from "react";
import { usePathname } from "next/navigation";
import { http } from "@/lib/http";
import { useAuth } from "@/features/auth/providers/auth-provider";
import { useSetAgentIds } from "./agent-status-provider";
import { useSetSessionIds } from "./session-status-provider";
import type { Agent, Session } from "@/types";

export interface SidebarAgentGroup {
  agent: Agent;
  sessions: Session[];
}

export type SidebarViewMode = "agent" | "date";

const VIEW_MODE_STORAGE_KEY = "aviary_sidebar_view_mode";

interface SidebarContextValue {
  groups: SidebarAgentGroup[];
  loading: boolean;
  collapsed: boolean;
  toggleCollapsed: () => void;
  viewMode: SidebarViewMode;
  setViewMode: (mode: SidebarViewMode) => void;
  refresh: () => Promise<void>;
  updateSessionTitle: (sessionId: string, title: string) => void;
  deleteSession: (sessionId: string) => Promise<void>;
}

const SidebarContext = createContext<SidebarContextValue | null>(null);

/**
 * SidebarProvider — owns the data for the sidebar (agents + their active sessions).
 *
 * Lifted out of the AppShell component so that:
 *   1. Page-level components (e.g. ChatView) can call updateSessionTitle()
 *      without prop drilling.
 *   2. Polling provider feeds (agent / session status) get clean ID lists.
 *   3. The visual AppShell can stay purely presentational.
 */
export function SidebarProvider({ children }: { children: React.ReactNode }) {
  const { user } = useAuth();
  const pathname = usePathname();
  const [groups, setGroups] = useState<SidebarAgentGroup[]>([]);
  const [loading, setLoading] = useState(true);
  const [collapsed, setCollapsed] = useState(false);
  const [viewMode, setViewModeState] = useState<SidebarViewMode>("agent");
  const setAgentIds = useSetAgentIds();
  const setSessionIds = useSetSessionIds();

  // Read persisted viewMode after mount (avoid SSR localStorage access)
  useEffect(() => {
    if (typeof window === "undefined") return;
    const stored = window.localStorage.getItem(VIEW_MODE_STORAGE_KEY);
    if (stored === "date" || stored === "agent") {
      setViewModeState(stored);
    }
  }, []);

  const setViewMode = useCallback((mode: SidebarViewMode) => {
    setViewModeState(mode);
    if (typeof window !== "undefined") {
      window.localStorage.setItem(VIEW_MODE_STORAGE_KEY, mode);
    }
  }, []);

  const refresh = useCallback(async () => {
    if (!user) return;
    try {
      const agentsRes = await http.get<{ items: Agent[] }>("/agents");
      const agents = agentsRes.items;

      const withSessions = await Promise.all(
        agents.map(async (agent) => {
          const sessionsRes = await http
            .get<{ items: Session[] }>(`/agents/${agent.id}/sessions`)
            .catch(() => ({ items: [] as Session[] }));
          return {
            agent,
            sessions: sessionsRes.items.filter((s) => s.status === "active"),
          };
        }),
      );
      setGroups(withSessions);
    } finally {
      setLoading(false);
    }
  }, [user]);

  useEffect(() => {
    refresh();
  }, [refresh]);

  // Refresh sidebar when entering a session URL (a session may have been
  // just created on a different page; this catches it without prop drilling).
  useEffect(() => {
    if (pathname.startsWith("/sessions/")) {
      refresh();
    }
  }, [pathname, refresh]);

  // Push IDs into the polling providers
  useEffect(() => {
    setSessionIds(groups.flatMap((g) => g.sessions).map((s) => s.id));
  }, [groups, setSessionIds]);

  useEffect(() => {
    setAgentIds(groups.filter((g) => g.sessions.length > 0).map((g) => g.agent.id));
  }, [groups, setAgentIds]);

  const updateSessionTitle = useCallback((sessionId: string, title: string) => {
    setGroups((prev) =>
      prev.map((g) => ({
        ...g,
        sessions: g.sessions.map((s) => (s.id === sessionId ? { ...s, title } : s)),
      })),
    );
  }, []);

  const deleteSession = useCallback(async (sessionId: string) => {
    await http.delete(`/sessions/${sessionId}`);
    setGroups((prev) =>
      prev.map((g) => ({
        ...g,
        sessions: g.sessions.filter((s) => s.id !== sessionId),
      })),
    );
  }, []);

  const toggleCollapsed = useCallback(() => setCollapsed((c) => !c), []);

  return (
    <SidebarContext.Provider
      value={{
        groups,
        loading,
        collapsed,
        toggleCollapsed,
        viewMode,
        setViewMode,
        refresh,
        updateSessionTitle,
        deleteSession,
      }}
    >
      {children}
    </SidebarContext.Provider>
  );
}

export function useSidebar(): SidebarContextValue {
  const ctx = useContext(SidebarContext);
  if (!ctx) throw new Error("useSidebar must be used within SidebarProvider");
  return ctx;
}
