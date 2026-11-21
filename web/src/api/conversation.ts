import { useQuery } from "@tanstack/react-query";
import { queryClient } from "../query.ts";
import { api, ApiError, isLast, type TurnEvent } from "./client.ts";
import {
  readSession,
  readSessions,
  type SessionPage,
  take,
  useFeedReady,
} from "./feed.ts";
import { add, NOTHING } from "./items.ts";
import type { ApprovalRequest, Session, Turn, Usage } from "./types.ts";

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
  const ready = useFeedReady();
  return useQuery({
    queryKey: ["session", id],
    queryFn: () => readSession(id),
    enabled: ready,
  });
}

// useTurns reads the newest turns of a session, oldest first: a page at
// first, and as many as olderTurns() has added since. followTurn() keeps
// the one that has not ended as it is.
export function useTurns(session: string) {
  return useQuery({
    queryKey: turnsKey(session),
    queryFn: async () => {
      const { turns } = await api.get<{ turns: Turn[] }>(
        `/v1/sessions/${session}/turns?limit=${PAGE}`,
      );
      // A turn that has not ended keeps what its events have told, which
      // its record does not have.
      const shown = queryClient.getQueryData<Turn[]>(turnsKey(session)) ?? [];
      const read = turns.reverse().map((turn) => {
        const told = shown.find((other) => other.id === turn.id);
        return turn.ended_at || !told
          ? turn
          : { ...turn, items: told.items, position: told.position };
      });
      // A turn that was added while the turns were read stays.
      const last = read.at(-1)?.seq ?? 0;
      return [...read, ...shown.filter((turn) => turn.seq > last)];
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

// startSession creates a session of an agent with a title, and keeps it as
// if it had been read: its conversation opens without a wait.
export async function startSession(
  agent: string,
  title: string,
): Promise<Session> {
  const session = await api.post<Session>(`/v1/agents/${agent}/sessions`, {
    title,
  });
  queryClient.setQueryData<Session>(["session", session.id], session);
  queryClient.setQueryData<Turn[]>(turnsKey(session.id), []);
  take(session);
  return session;
}

// sendMessage sends a message to a session, and adds the turn that it
// begins to the turns that were read.
export async function sendMessage(
  session: string,
  text: string,
): Promise<Turn> {
  const turn = await api.post<Turn>(`/v1/sessions/${session}/messages`, {
    text,
  });
  // The session may have told of the turn already.
  queryClient.setQueryData<Turn[]>(turnsKey(session), (list = []) =>
    list.some((other) => other.id === turn.id) ? list : [...list, turn],
  );
  return turn;
}

// stopTurn asks for a turn to be stopped, or to be taken from the queue,
// and keeps the turn as the API then has it. A turn that runs goes on until
// its harness has stopped: its events say when.
export async function stopTurn(session: string, id: string): Promise<void> {
  let record: Turn;
  try {
    record = await api.post<Turn>(`/v1/turns/${id}/cancel`);
  } catch (error) {
    // The turn ended by itself meanwhile.
    if (error instanceof ApiError && error.code === "turn_ended") return;
    throw error;
  }
  queryClient.setQueryData<Turn[]>(turnsKey(session), (list) =>
    list?.map((turn) =>
      turn.id === id && !turn.ended_at
        ? { ...record, items: turn.items, position: turn.position }
        : turn,
    ),
  );
}

// answerRequest answers a request of a turn for approval with one of its
// options. The events of the turn say when the harness has the answer. It
// reports whether the answer was taken: a request has one answer, and
// someone else may have given it.
export async function answerRequest(
  turn: string,
  request: string,
  option: string,
): Promise<boolean> {
  try {
    await api.post(`/v1/turns/${turn}/approvals/${request}`, {
      option_id: option,
    });
    return true;
  } catch (error) {
    if (error instanceof ApiError && error.code === "approval_answered") {
      return false;
    }
    throw error;
  }
}

// rereadSession reads a session and its turns again, after the API said
// that they are not as the console has them.
export function rereadSession(session: string): Promise<unknown> {
  return Promise.all([
    queryClient.invalidateQueries({ queryKey: ["session", session] }),
    queryClient.invalidateQueries({ queryKey: turnsKey(session) }),
  ]);
}

// joinTurn reads the turn with the ID, which the session got after its
// turns were read, and adds it to them.
export async function joinTurn(session: string, id: string): Promise<void> {
  const key = turnsKey(session);
  const turn = await api.get<Turn>("/v1/turns/" + id);
  const list = queryClient.getQueryData<Turn[]>(key);
  if (!list || list.some((other) => other.id === turn.id)) return;
  // If the turn is not the next one, more than it are missing.
  if (turn.seq !== (list.at(-1)?.seq ?? 0) + 1) {
    return queryClient.invalidateQueries({ queryKey: key });
  }
  queryClient.setQueryData<Turn[]>(key, [...list, turn]);
}

// followTurn follows the events of a turn that has not ended. It keeps the
// turn, among the turns that were read, as its events say it stands and
// with what the agent has said and done so far, and puts the record of the
// turn in its place once the turn has ended. It returns a function that
// stops following.
export function followTurn(
  session: string,
  id: string,
  onError: (error: ApiError) => void,
): () => void {
  const change = (next: (turn: Turn) => Turn) =>
    queryClient.setQueryData<Turn[]>(turnsKey(session), (list) =>
      list?.map((turn) => (turn.id === id ? next(turn) : turn)),
    );
  // The events come from the first of the turn, so what the turn has said
  // is assembled from nothing.
  let said = NOTHING;
  return api.events(id, {
    onEvent: (event) => {
      const before = said;
      said = add(said, event.type, event.data);
      const now = new Date().toISOString();
      change((turn) => {
        // The record of a turn that has ended says more than its events.
        if (turn.ended_at) return turn;
        if (said === before) return advance(turn, event, now);
        const waiting = said.items.filter(
          (item) => item.type === "approval" && !item.outcome,
        );
        return {
          // An agent that says or does something has the message.
          ...advance(turn, { type: "turn.started", data: {} }, now),
          items: said.items,
          approvals: waiting as ApprovalRequest[],
        };
      });
      if (isLast(event.type)) {
        api
          .get<Turn>("/v1/turns/" + id)
          .then((record) => change(() => record), onError);
      }
    },
    onError,
  });
}

// advance returns a turn as it stands after an event that says where the
// turn is. now is the time, which these events do not carry.
export function advance(turn: Turn, event: TurnEvent, now: string): Turn {
  const ended = { ended_at: turn.ended_at ?? now };
  switch (event.type) {
    case "turn.queued":
      return { ...turn, position: Number(event.data.position) || 0 };
    case "turn.started":
      return {
        ...turn,
        state: "running",
        started_at: turn.started_at ?? now,
      };
    case "turn.completed":
      return {
        ...turn,
        ...ended,
        state: "completed",
        stop_reason: event.data.stop_reason as string | undefined,
        usage: event.data.usage as Usage | undefined,
      };
    case "turn.failed":
      return {
        ...turn,
        ...ended,
        state: "failed",
        failure: event.data as Turn["failure"],
      };
    case "turn.cancelled":
      return { ...turn, ...ended, state: "cancelled" };
    case "turn.expired":
      return { ...turn, ...ended, state: "expired" };
    default:
      return turn;
  }
}
