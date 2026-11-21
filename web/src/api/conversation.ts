import { useQuery } from "@tanstack/react-query";
import { queryClient } from "../query.ts";
import { api } from "./client.ts";
import { readSessions, type SessionPage, useFeedReady } from "./feed.ts";
import type { Session, Turn } from "./types.ts";

// How many sessions or turns the lists read at a time.
const PAGE = 20;

const listKey = (agent: string) => ["sessions", agent, "list"];
const turnsKey = (session: string) => ["turns", session];

// useSessionList reads the sessions of an agent, newest first: a page at
// first, and as many as olderSessions() has added since. The feed keeps
// them as they are.
export function useSessionList(agent: string) {
  const ready = useFeedReady();
  return useQuery({
    queryKey: listKey(agent),
    queryFn: () => {
      // What is read again is what was shown, as far as one request reads.
      const shown = queryClient.getQueryData<SessionPage>(listKey(agent));
      return readSessions(agent, Math.max(shown?.sessions.length ?? 0, PAGE));
    },
    enabled: ready,
  });
}

// olderSessions reads the page after the sessions that are listed, and adds
// it.
export async function olderSessions(agent: string): Promise<void> {
  const key = listKey(agent);
  const last = queryClient.getQueryData<SessionPage>(key)?.sessions.at(-1);
  if (!last) return;
  const older = await readSessions(agent, PAGE, last.id);
  // The list may have been read again meanwhile, and end elsewhere.
  queryClient.setQueryData<SessionPage>(key, (list) =>
    list?.sessions.at(-1)?.id === last.id
      ? { sessions: [...list.sessions, ...older.sessions], more: older.more }
      : list,
  );
}

// useSession reads a session. The feed keeps it as it is.
export function useSession(id: string) {
  return useQuery({
    queryKey: ["session", id],
    queryFn: () => api.get<Session>("/v1/sessions/" + id),
  });
}

// useTurns reads the newest turns of a session, oldest first: a page at
// first, and as many as olderTurns() has added since.
export function useTurns(session: string) {
  return useQuery({
    queryKey: turnsKey(session),
    queryFn: async () => {
      const { turns } = await api.get<{ turns: Turn[] }>(
        `/v1/sessions/${session}/turns?limit=${PAGE}`,
      );
      return turns.reverse();
    },
    // The turns are read when their session is opened, and not again by
    // themselves: that would drop the older ones that were added.
    refetchOnMount: "always",
    staleTime: Infinity,
  });
}

// olderTurns reads the page before the turns that are shown, and adds it.
export async function olderTurns(session: string): Promise<void> {
  const key = turnsKey(session);
  const first = queryClient.getQueryData<Turn[]>(key)?.[0];
  if (!first) return;
  const { turns } = await api.get<{ turns: Turn[] }>(
    `/v1/sessions/${session}/turns?limit=${PAGE}&before=${first.seq}`,
  );
  // The turns may have been read again meanwhile, and begin elsewhere.
  queryClient.setQueryData<Turn[]>(key, (list) =>
    list?.[0]?.seq === first.seq ? [...turns.reverse(), ...list] : list,
  );
}
