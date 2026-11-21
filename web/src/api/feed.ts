import { useEffect, useSyncExternalStore } from "react";
import { queryClient } from "../query.ts";
import { api } from "./client.ts";
import type { Session } from "./types.ts";

// The feed keeps what the console has read of the sessions of a project up
// to date. It follows the stream of the project's changes, and puts every
// session that changes where the console keeps the sessions that it read.
//
// The stream does not repeat what happened before it opened. So the sessions
// are read once the stream is open, and read again each time it has opened
// again.

// What the stream has told of each session, and when: a count that grows
// with every session told.
const told = new Map<string, { session: Session; at: number }>();
let clock = 0;
// Whether sessions can be read without missing a change: the stream is
// open, or cannot be opened.
let ready = false;
const listeners = new Set<() => void>();

function setReady(value: boolean) {
  ready = value;
  for (const listener of listeners) listener();
}

// take puts a session where the console keeps what it read: in the lists of
// its agent that have it, and at the top of those that it is newer than. An
// older session that a list does not have comes with its page.
export function take(session: Session) {
  told.set(session.id, { session, at: ++clock });
  queryClient.setQueriesData<Session[]>(
    { queryKey: ["sessions", session.agent_id] },
    (list) => {
      if (!list) return list;
      if (list.some((other) => other.id === session.id)) {
        return list.map((other) => (other.id === session.id ? session : other));
      }
      return !list.length || session.id > list[0].id
        ? [session, ...list]
        : list;
    },
  );
}

// readSessions reads a page of the sessions of an agent, newest first. A
// session that changes while the page is read is returned as the stream
// told it, which is newer, and so is one that is created meanwhile.
export async function readSessions(
  agent: string,
  query: string,
): Promise<Session[]> {
  const since = clock;
  const { sessions } = await api.get<{ sessions: Session[] }>(
    `/v1/agents/${agent}/sessions?${query}`,
  );
  const newer = [...told.values()].filter((each) => each.at > since);
  const page = sessions.map(
    (session) =>
      newer.find((each) => each.session.id === session.id)?.session ?? session,
  );
  if (query.includes("before=")) return page;
  const newest = sessions[0]?.id ?? "";
  const created = newer
    .map((each) => each.session)
    .filter((session) => session.agent_id === agent && session.id > newest)
    .sort((a, b) => (a.id < b.id ? 1 : -1));
  return [...created, ...page];
}

// useFeed follows the changes of the project with the ID, while there is
// one.
export function useFeed(project: string | undefined) {
  useEffect(() => {
    if (!project) return;
    let opened = false;
    const stop = api.changes(project, {
      onSession: take,
      onOpen: () => {
        if (opened)
          void queryClient.invalidateQueries({ queryKey: ["sessions"] });
        opened = true;
        setReady(true);
      },
      // Without the stream the console shows what it reads, and nothing
      // moves.
      onError: () => setReady(true),
    });
    return () => {
      stop();
      told.clear();
      setReady(false);
    };
  }, [project]);
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

// useFeedReady reports whether the sessions of the project can be read
// without missing a change.
export function useFeedReady(): boolean {
  return useSyncExternalStore(subscribe, () => ready);
}
