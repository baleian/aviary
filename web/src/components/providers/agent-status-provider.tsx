"use client";

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
} from "react";
import { apiFetch } from "@/lib/api";

type AgentReadiness = "ready" | "offline";

interface AgentStatusContextValue {
  statuses: Record<string, AgentReadiness>;
  agentIds: string[];
  setAgentIds: (ids: string[]) => void;
}

const AgentStatusContext = createContext<AgentStatusContextValue>({
  statuses: {},
  agentIds: [],
  setAgentIds: () => {},
});

const POLL_INTERVAL = 10000;

export function AgentStatusProvider({ children }: { children: React.ReactNode }) {
  const [agentIds, setAgentIds] = useState<string[]>([]);
  const [statuses, setStatuses] = useState<Record<string, AgentReadiness>>({});
  const intervalRef = useRef<ReturnType<typeof setInterval> | null>(null);

  const poll = useCallback(async () => {
    if (agentIds.length === 0) return;

    try {
      const res = await apiFetch<{
        statuses: Record<string, string>;
      }>(`/agents/status?ids=${agentIds.join(",")}`);

      const merged: Record<string, AgentReadiness> = {};
      for (const id of agentIds) {
        merged[id] = (res.statuses[id] === "ready" ? "ready" : "offline") as AgentReadiness;
      }
      setStatuses(merged);
    } catch {
      // Silently fail
    }
  }, [agentIds]);

  useEffect(() => {
    poll();

    if (intervalRef.current) clearInterval(intervalRef.current);
    intervalRef.current = setInterval(poll, POLL_INTERVAL);

    return () => {
      if (intervalRef.current) clearInterval(intervalRef.current);
    };
  }, [poll]);

  return (
    <AgentStatusContext.Provider value={{ statuses, agentIds, setAgentIds }}>
      {children}
    </AgentStatusContext.Provider>
  );
}

export function useAgentStatus(agentId: string): AgentReadiness {
  const { statuses } = useContext(AgentStatusContext);
  return statuses[agentId] || "offline";
}

export function useSetAgentIds() {
  const { setAgentIds } = useContext(AgentStatusContext);
  return setAgentIds;
}
