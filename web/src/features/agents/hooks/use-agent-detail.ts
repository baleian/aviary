"use client";

import { useCallback, useEffect, useState } from "react";
import { agentsApi } from "@/features/agents/api/agents-api";
import { useUserEvents } from "@/features/events/user-events-provider";
import { extractErrorMessage } from "@/lib/http";
import type { Agent, Session } from "@/types";

export interface AgentDetailData {
  agent: Agent | null;
  sessions: Session[];
  loading: boolean;
  error: string | null;
  refresh: () => Promise<void>;
  createSession: () => Promise<Session | null>;
  creating: boolean;
  createError: string | null;
}

/**
 * Loads an agent + its active sessions for the detail page. The chat tab
 * keeps a live sessions list (per-agent), so this hook owns both.
 */
export function useAgentDetail(agentId: string): AgentDetailData {
  const [agent, setAgent] = useState<Agent | null>(null);
  const [sessions, setSessions] = useState<Session[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [createError, setCreateError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    setError(null);
    try {
      const [a, s] = await Promise.all([
        agentsApi.get(agentId),
        agentsApi.listSessions(agentId).catch(() => ({ items: [] as Session[] })),
      ]);
      setAgent(a);
      setSessions(s.items.filter((x) => x.status === "active"));
    } catch (e) {
      setError(extractErrorMessage(e));
    }
  }, [agentId]);

  useEffect(() => {
    let alive = true;
    setLoading(true);
    refresh().finally(() => {
      if (alive) setLoading(false);
    });
    return () => {
      alive = false;
    };
  }, [refresh]);

  const createSession = useCallback(async (): Promise<Session | null> => {
    if (creating) return null;
    setCreating(true);
    setCreateError(null);
    try {
      const session = await agentsApi.createSession(agentId);
      setSessions((prev) =>
        prev.some((s) => s.id === session.id) ? prev : [session, ...prev],
      );
      return session;
    } catch (e) {
      setCreateError(extractErrorMessage(e));
      return null;
    } finally {
      setCreating(false);
    }
  }, [agentId, creating]);

  useUserEvents(
    useCallback(
      (event) => {
        if (event.type === "session_created") {
          if (event.session.agent_id !== agentId) return;
          if (event.session.status !== "active") return;
          const incoming = event.session;
          setSessions((prev) =>
            prev.some((s) => s.id === incoming.id)
              ? prev
              : [
                  {
                    id: incoming.id,
                    agent_id: incoming.agent_id,
                    title: incoming.title,
                    status: incoming.status,
                    created_at: incoming.created_at,
                    last_message_at: incoming.last_message_at,
                  } as Session,
                  ...prev,
                ],
          );
          return;
        }
        if (event.type === "session_deleted") {
          setSessions((prev) => prev.filter((s) => s.id !== event.session_id));
          return;
        }
        if (event.type === "session_changed" && event.title != null) {
          setSessions((prev) =>
            prev.map((s) =>
              s.id === event.session_id ? { ...s, title: event.title ?? s.title } : s,
            ),
          );
        }
      },
      [agentId],
    ),
  );

  return {
    agent,
    sessions,
    loading,
    error,
    refresh,
    createSession,
    creating,
    createError,
  };
}
