import { useQuery } from "@tanstack/react-query";
import { clip } from "../lib/format.ts";
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
import type {
  ApprovalRequest,
  Attachment,
  Limits,
  Session,
  Turn,
  Usage,
} from "./types.ts";

// How many sessions or turns the lists read at a time.
const PAGE = 20;

const listKey = (agent: string) => ["sessions", agent, "list"];
const turnsKey = (session: string) => ["turns", session];

// The titles that the console gave to sessions: to one that it named after
// its first message, and to one that took what its harness suggested. A
// session that a person named here has false.
const given = new Map<string, string | false>();

// The longest title that the console takes from a harness.
const SUGGESTED = 120;

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
// if it had been read: its conversation opens without a wait. servers are the
// MCP servers that it begins with, if they are not its agent's.
export async function startSession(
  agent: string,
  title: string,
  servers?: string[],
): Promise<Session> {
  const session = await api.post<Session>(`/v1/agents/${agent}/sessions`, {
    title,
    ...(servers && { mcp_servers: servers }),
  });
  queryClient.setQueryData<Session>(["session", session.id], session);
  queryClient.setQueryData<Turn[]>(turnsKey(session.id), []);
  take(session);
  given.set(session.id, session.title);
  return session;
}

// renameSession gives a session the title that a person wrote.
export async function renameSession(id: string, title: string) {
  take(await api.patch<Session>("/v1/sessions/" + id, { title }));
  given.set(id, false);
}

// suggestTitle takes a title that the harness of a session suggests for
// it. It replaces no title that a person wrote: a session takes it only
// while it has none, or the one that the console gave.
export async function suggestTitle(id: string, title: unknown) {
  if (typeof title !== "string" || !title.trim()) return;
  const session = queryClient.getQueryData<Session>(["session", id]);
  const mine = given.get(id);
  if (!session || mine === false) return;
  if (session.title && session.title !== mine) return;
  const renamed = await api.patch<Session>("/v1/sessions/" + id, {
    title: clip(title, SUGGESTED),
  });
  take(renamed);
  given.set(id, renamed.title);
}

// setMcpServer turns an MCP server of the project on or off for the next
// turns of a session. What is shown changes at once, and is read again if
// the API refuses the change.
export async function setMcpServer(
  session: Session,
  server: string,
  enabled: boolean,
): Promise<void> {
  const before = session.mcp_servers ?? [];
  const after = enabled
    ? [...before, server]
    : before.filter((other) => other !== server);
  queryClient.setQueryData<Session>(
    ["session", session.id],
    (read) => read && { ...read, mcp_servers: after },
  );
  try {
    take(
      await api.put<Session>(
        `/v1/sessions/${session.id}/mcp-servers/${server}`,
        { enabled },
      ),
    );
  } catch (error) {
    await rereadSession(session.id);
    throw error;
  }
}

// resetMcpServers has a session go back to the MCP servers of its agent.
export async function resetMcpServers(session: Session): Promise<void> {
  take(await api.delete<Session>(`/v1/sessions/${session.id}/mcp-servers`));
}

// endSession ends a session, with the files that its agent made.
export async function endSession(id: string) {
  take(await api.delete<Session>("/v1/sessions/" + id));
}

// useLimits reads what the installation sets for the files that messages
// carry.
export function useLimits() {
  return useQuery({
    queryKey: ["limits"],
    queryFn: () => api.get<Limits>("/v1/limits"),
    staleTime: Infinity,
  });
}

// uploadAttachment uploads a file for a message of the session to carry, and
// returns the attachment. onProgress is called with the bytes of the file
// that have gone, as they go.
export function uploadAttachment(
  session: string,
  file: File,
  onProgress?: (sent: number) => void,
): Promise<Attachment> {
  const name = encodeURIComponent(file.name);
  return api.upload<Attachment>(
    `/v1/sessions/${session}/attachments?name=${name}`,
    file,
    onProgress,
  );
}

// sendMessage sends a message to a session, with the files that were
// uploaded for it, by the IDs of their attachments and in their order. It
// adds the turn that the message begins to the turns that were read.
export async function sendMessage(
  session: string,
  text: string,
  attachments: string[] = [],
): Promise<Turn> {
  const turn = await api.post<Turn>(`/v1/sessions/${session}/messages`, {
    text,
    ...(attachments.length > 0 && { attachments }),
  });
  // The session may have told of the turn already.
  queryClient.setQueryData<Turn[]>(turnsKey(session), (list = []) =>
    list.some((other) => other.id === turn.id) ? list : [...list, turn],
  );
  return turn;
}

// saveAttachment reads the file of an attachment, and has the browser save
// it under its name. The API gives the file only to a caller that it knows,
// so a link to it would not do.
export async function saveAttachment(attachment: Attachment): Promise<void> {
  const file = await api.download("/v1/attachments/" + attachment.id);
  const address = URL.createObjectURL(file);
  const link = document.createElement("a");
  link.href = address;
  link.download = attachment.name;
  link.click();
  // The browser has begun to save the file by now.
  setTimeout(() => URL.revokeObjectURL(address), 1000);
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
        ? {
            ...record,
            // The answer has the turn without what its record has besides.
            attachments: turn.attachments,
            items: turn.items,
            position: turn.position,
          }
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
// turn in its place once the turn has ended. onTitle is called with a title
// that the harness suggests for the session. It returns a function that
// stops following.
export function followTurn(
  session: string,
  id: string,
  onError: (error: ApiError) => void,
  onTitle?: (title: unknown) => void,
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
      if (event.type === "session.update") onTitle?.(event.data.title);
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
      if (isLast(event.type)) readEnded(session, id).catch(onError);
    },
    onError,
  });
}

// The turns whose records are being read because they have ended.
const ending = new Map<string, Promise<void>>();

// readEnded puts the record of a turn that has ended in the place of the
// turn, among the turns that were read. A turn is read once at a time,
// whoever asks.
function readEnded(session: string, id: string): Promise<void> {
  let reading = ending.get(id);
  if (!reading) {
    reading = api
      .get<Turn>("/v1/turns/" + id)
      .then((record) => {
        queryClient.setQueryData<Turn[]>(turnsKey(session), (list) =>
          list?.map((turn) => (turn.id === id ? record : turn)),
        );
      })
      .finally(() => ending.delete(id));
    ending.set(id, reading);
  }
  return reading;
}

// noteTurn takes what a session says of its newest turn, as the stream of
// the project tells it. The events of a turn do not say when the turn
// leaves the queue, nor that it was told to stop from elsewhere: its
// session does. A turn only moves on: what its events told stays.
export function noteTurn(session: string, last: Turn): Promise<void> {
  const key = turnsKey(session);
  const list = queryClient.getQueryData<Turn[]>(key);
  const shown = list?.find((turn) => turn.id === last.id);
  if (!shown || shown.ended_at) return Promise.resolve();
  // The turn has ended, and its events have not said so yet.
  if (last.ended_at) return readEnded(session, last.id);
  queryClient.setQueryData<Turn[]>(key, (turns) =>
    turns?.map((turn) =>
      turn.id !== last.id || turn.ended_at
        ? turn
        : {
            ...turn,
            state: turn.state === "queued" ? last.state : turn.state,
            started_at: turn.started_at ?? last.started_at,
            cancel_requested_at:
              turn.cancel_requested_at ?? last.cancel_requested_at,
          },
    ),
  );
  return Promise.resolve();
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
