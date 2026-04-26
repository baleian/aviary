"use client";

import { createContext, useCallback, useContext, useEffect, useRef, useState } from "react";
import { http } from "@/lib/http";
import { useUserEvents } from "@/features/events/user-events-provider";

interface SessionStatusEntry {
  status: "streaming" | "idle" | "offline";
  unread: number;
  title: string | null;
}

interface SessionStatusContextValue {
  statuses: Record<string, SessionStatusEntry>;
  setSessionIds: (ids: string[]) => void;
}

const SessionStatusContext = createContext<SessionStatusContextValue | null>(null);

const DEFAULT_ENTRY: SessionStatusEntry = { status: "offline", unread: 0, title: null };

export function SessionStatusProvider({ children }: { children: React.ReactNode }) {
  const [sessionIds, setSessionIds] = useState<string[]>([]);
  const [statuses, setStatuses] = useState<Record<string, SessionStatusEntry>>({});
  const lastIdsKeyRef = useRef<string>("");

  // Snapshot covers in-flight streams and unread on first load; live updates
  // arrive via the user-events WebSocket.
  useEffect(() => {
    if (sessionIds.length === 0) return;
    const key = sessionIds.slice().sort().join(",");
    if (key === lastIdsKeyRef.current) return;
    lastIdsKeyRef.current = key;
    let alive = true;
    (async () => {
      try {
        const res = await http.get<{
          statuses: Record<string, string>;
          unread: Record<string, number>;
          titles: Record<string, string | null>;
        }>(`/sessions/status?ids=${sessionIds.join(",")}`);
        if (!alive) return;
        setStatuses((prev) => {
          const next = { ...prev };
          for (const id of sessionIds) {
            next[id] = {
              status: (res.statuses[id] || "offline") as SessionStatusEntry["status"],
              unread: res.unread[id] || 0,
              title: res.titles[id] ?? prev[id]?.title ?? null,
            };
          }
          return next;
        });
      } catch (err) {
        console.warn("[session-status] snapshot failed", err);
      }
    })();
    return () => {
      alive = false;
    };
  }, [sessionIds]);

  useUserEvents(
    useCallback((event) => {
      if (event.type !== "session_changed") return;
      setStatuses((prev) => {
        const current = prev[event.session_id] ?? { ...DEFAULT_ENTRY };
        const next: SessionStatusEntry = {
          status: event.status ?? current.status,
          unread: event.unread ?? current.unread,
          title: event.title ?? current.title,
        };
        return { ...prev, [event.session_id]: next };
      });
    }, []),
  );

  return (
    <SessionStatusContext.Provider value={{ statuses, setSessionIds }}>
      {children}
    </SessionStatusContext.Provider>
  );
}

function useSessionStatusContext() {
  const ctx = useContext(SessionStatusContext);
  if (!ctx) throw new Error("useSessionStatus must be used within SessionStatusProvider");
  return ctx;
}

export function useSessionStatus(sessionId: string): SessionStatusEntry {
  return useSessionStatusContext().statuses[sessionId] || DEFAULT_ENTRY;
}

export function useSetSessionIds() {
  return useSessionStatusContext().setSessionIds;
}
