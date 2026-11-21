// @vitest-environment happy-dom
import { QueryClientProvider } from "@tanstack/react-query";
import { cleanup, renderHook, waitFor } from "@testing-library/react";
import { createElement, type ReactNode } from "react";
import { afterEach, expect, it, vi } from "vitest";
import { queryClient } from "../query.ts";
import { api, ApiError, type TurnEvent } from "./client.ts";
import {
  advance,
  followTurn,
  joinTurn,
  olderSessions,
  olderTurns,
  rereadSession,
  sendMessage,
  startSession,
  stopTurn,
  useTurns,
} from "./conversation.ts";
import type { SessionPage } from "./feed.ts";
import type { Session, Turn } from "./types.ts";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  queryClient.clear();
});

// answering returns how to have the API answer the next request.
function answering() {
  let answer!: (body: unknown) => void;
  vi.spyOn(api, "get").mockReturnValue(
    new Promise((resolve) => {
      answer = resolve;
    }),
  );
  return (body: unknown) => answer(body);
}

const TURNS = ["turns", "s1"];
const turn = (seq: number, more: Partial<Turn> = {}) =>
  ({ id: "t" + seq, seq, ...more }) as Turn;
const seqs = () =>
  queryClient.getQueryData<Turn[]>(TURNS)!.map((each) => each.seq);

it("adds earlier turns before the turns that they were read for", async () => {
  queryClient.setQueryData(TURNS, [turn(21), turn(22)]);
  let answer = answering();
  let read = olderTurns("s1");
  expect(api.get).toHaveBeenCalledWith(
    "/v1/sessions/s1/turns?limit=20&before=21",
  );
  answer({ turns: [turn(20), turn(19)] });
  await read;
  expect(seqs()).toEqual([19, 20, 21, 22]);

  // The turns are read again while the earlier ones are on their way.
  answer = answering();
  read = olderTurns("s1");
  queryClient.setQueryData(TURNS, [turn(41), turn(42)]);
  answer({ turns: [turn(18), turn(17)] });
  await read;
  expect(seqs()).toEqual([41, 42]);
});

const LIST = ["sessions", "a1", "list"];
const session = (id: string) => ({ id, agent_id: "a1" }) as Session;
const listed = () => {
  const page = queryClient.getQueryData<SessionPage>(LIST)!;
  return [page.sessions.map((each) => each.id), page.more];
};

it("adds older sessions after the sessions that they were read for", async () => {
  queryClient.setQueryData<SessionPage>(LIST, {
    sessions: [session("s9"), session("s8")],
    more: true,
  });
  let answer = answering();
  let read = olderSessions("a1");
  expect(api.get).toHaveBeenCalledWith(
    "/v1/agents/a1/sessions?limit=21&before=s8",
  );
  answer({ sessions: [session("s7"), session("s6")] });
  await read;
  expect(listed()).toEqual([["s9", "s8", "s7", "s6"], false]);

  // The list is read again while the older ones are on their way.
  answer = answering();
  read = olderSessions("a1");
  queryClient.setQueryData<SessionPage>(LIST, {
    sessions: [session("s9"), session("s8")],
    more: true,
  });
  answer({ sessions: [session("s5")] });
  await read;
  expect(listed()).toEqual([["s9", "s8"], true]);
});

it("adds a turn that the session got to the turns that were read", async () => {
  queryClient.setQueryData(TURNS, []);
  const invalidate = vi
    .spyOn(queryClient, "invalidateQueries")
    .mockResolvedValue();
  const join = async (seq: number) => {
    const answer = answering();
    const joined = joinTurn("s1", "t" + seq);
    expect(api.get).toHaveBeenCalledWith("/v1/turns/t" + seq);
    answer(turn(seq));
    await joined;
  };
  await join(1);
  await join(2);
  // A turn that is there is left alone.
  await join(2);
  await join(1);
  expect(seqs()).toEqual([1, 2]);
  expect(invalidate).not.toHaveBeenCalled();

  // With more turns missing than the one, all are read again.
  await join(5);
  expect(seqs()).toEqual([1, 2]);
  expect(invalidate).toHaveBeenCalledWith({ queryKey: TURNS });
});

const NOW = "2026-11-22T10:00:05.000Z";
const event = (
  type: string,
  data: Record<string, unknown> = {},
): TurnEvent => ({
  type,
  data,
});

it("says where a turn is after each event that tells it", () => {
  const queued = turn(1, { state: "queued" });
  expect(advance(queued, event("turn.queued", { position: 2 }), NOW)).toEqual({
    ...queued,
    position: 2,
  });
  expect(advance(queued, event("turn.started"), NOW)).toEqual({
    ...queued,
    state: "running",
    started_at: NOW,
  });
  const running = turn(1, { state: "running", started_at: "earlier" });
  // A time that the turn has is kept.
  expect(advance(running, event("turn.started"), NOW)).toEqual(running);
  const ended = { ...running, ended_at: "later" };
  expect(advance(ended, event("turn.cancelled"), NOW).ended_at).toBe("later");
  const usage = { input_tokens: 5, output_tokens: 7 };
  expect(
    advance(
      running,
      event("turn.completed", { stop_reason: "end_turn", usage }),
      NOW,
    ),
  ).toEqual({
    ...running,
    state: "completed",
    stop_reason: "end_turn",
    usage,
    ended_at: NOW,
  });
  const failure = { reason: "timed_out", message: "too long" };
  expect(advance(running, event("turn.failed", failure), NOW)).toEqual({
    ...running,
    state: "failed",
    failure,
    ended_at: NOW,
  });
  expect(advance(running, event("turn.cancelled"), NOW)).toEqual({
    ...running,
    state: "cancelled",
    ended_at: NOW,
  });
  expect(advance(queued, event("turn.expired"), NOW)).toEqual({
    ...queued,
    state: "expired",
    ended_at: NOW,
  });
  expect(advance(running, event("message.delta", { text: "x" }), NOW)).toBe(
    running,
  );
});

// following follows the turn t2 of the turns that were read, and returns
// how to have an event of it come.
function following(onError = vi.fn()) {
  let tell!: (event: TurnEvent) => void;
  const stop = vi.fn();
  vi.spyOn(api, "events").mockImplementation((_turn, handlers) => {
    tell = handlers.onEvent;
    return stop;
  });
  expect(followTurn("s1", "t2", onError)).toBe(stop);
  expect(api.events).toHaveBeenCalledWith("t2", expect.anything());
  return (type: string, data: Record<string, unknown> = {}) =>
    tell({ type, data });
}

const read = () => queryClient.getQueryData<Turn[]>(TURNS)!;

it("keeps a turn as its events say it stands, with what it has said so far", () => {
  queryClient.setQueryData(TURNS, [turn(1), turn(2, { state: "queued" })]);
  const [first] = read();
  const tell = following();
  tell("turn.queued", { position: 2 });
  expect(read()[1]).toMatchObject({ state: "queued", position: 2 });
  tell("turn.started");
  expect(read()[1].state).toBe("running");
  const started = read()[1].started_at;
  expect(Date.parse(started!)).toBeGreaterThan(Date.now() - 5000);
  tell("message.delta", { text: "Hel" });
  tell("message.delta", { text: "lo" });
  expect(read()[1].items).toEqual([{ type: "message", text: "Hello" }]);
  expect(read()[1].started_at).toBe(started);

  // A request that waits makes the turn wait, until it has its answer.
  tell("approval.requested", { id: "1", options: [] });
  expect(read()[1].approvals).toMatchObject([{ id: "1" }]);
  tell("approval.resolved", { id: "1", outcome: "selected", option_id: "a" });
  expect(read()[1].approvals).toEqual([]);
  expect(read()[1].items!.length).toBe(2);

  // An event that is not of the turn changes nothing, and no other turn is
  // touched.
  const before = read();
  tell("session.update", { title: "A title" });
  expect(read()).toBe(before);
  expect(read()[0]).toBe(first);
});

it("leaves a turn as its record has it once the turn has ended", () => {
  const record = turn(2, {
    state: "completed",
    ended_at: "2026-11-22T10:00:09Z",
    items: [{ type: "message", text: "From the record." }],
  });
  queryClient.setQueryData(TURNS, [record]);
  // The events come again from the first, though the record was read
  // meanwhile.
  const tell = following();
  tell("turn.started");
  tell("message.delta", { text: "From" });
  expect(read()[0]).toBe(record);
});

it("takes an agent that says something for one that has the message", () => {
  queryClient.setQueryData(TURNS, [turn(2, { state: "queued" })]);
  const tell = following();
  tell("thought.delta", { text: "Hm." });
  expect(read()[0]).toMatchObject({
    state: "running",
    items: [{ type: "thought", text: "Hm." }],
  });
  expect(read()[0].started_at).toBeTruthy();
});

it("puts the record of a turn in its place once the turn has ended", async () => {
  queryClient.setQueryData(TURNS, [turn(2, { state: "running" })]);
  const onError = vi.fn();
  const tell = following(onError);
  tell("message.delta", { text: "Almost." });
  let answer = answering();
  tell("turn.completed", { stop_reason: "end_turn" });
  // The turn has ended at once, with what it said.
  expect(read()[0]).toMatchObject({
    state: "completed",
    stop_reason: "end_turn",
    items: [{ type: "message", text: "Almost." }],
  });
  expect(read()[0].ended_at).toBeTruthy();
  expect(api.get).toHaveBeenCalledWith("/v1/turns/t2");
  const record = turn(2, {
    state: "completed",
    ended_at: "2026-11-22T10:00:09Z",
    items: [{ type: "message", text: "From the record." }],
  });
  answer(record);
  await waitFor(() => expect(read()[0]).toEqual(record));
  expect(onError).not.toHaveBeenCalled();

  // A record that cannot be read leaves the turn as its events told it.
  queryClient.setQueryData(TURNS, [turn(2, { state: "running" })]);
  const failed = new Error("no answer");
  vi.spyOn(api, "get").mockRejectedValue(failed);
  answer = () => {};
  tell("turn.cancelled");
  await waitFor(() => expect(onError).toHaveBeenCalledWith(failed));
  expect(read()[0].state).toBe("cancelled");
});

it("keeps what the events of a turn told when the turns are read again", async () => {
  const told = turn(2, {
    state: "queued",
    position: 3,
    items: [{ type: "message", text: "So far." }],
  });
  queryClient.setQueryData(TURNS, [
    turn(1, { ended_at: "x", items: [{ type: "message", text: "Old." }] }),
    told,
  ]);
  // The API lists the newest turn first, and a turn that has not ended
  // without what it has said.
  const answer = answering();
  const wrapper = ({ children }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client: queryClient }, children);
  const { result } = renderHook(() => useTurns("s1"), { wrapper });
  expect(api.get).toHaveBeenCalledWith("/v1/sessions/s1/turns?limit=20");
  answer({
    turns: [
      turn(2, { state: "running", started_at: "y" }),
      turn(1, { ended_at: "x", items: [{ type: "message", text: "New." }] }),
    ],
  });
  await waitFor(() => expect(result.current.data![1].state).toBe("running"));
  expect(result.current.data).toEqual([
    turn(1, { ended_at: "x", items: [{ type: "message", text: "New." }] }),
    turn(2, {
      state: "running",
      started_at: "y",
      position: 3,
      items: [{ type: "message", text: "So far." }],
    }),
  ]);
});

it("keeps a turn that was added while the turns were read", async () => {
  queryClient.setQueryData(TURNS, [turn(1, { ended_at: "x" })]);
  const answer = answering();
  const wrapper = ({ children }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client: queryClient }, children);
  const { result } = renderHook(() => useTurns("s1"), { wrapper });
  // A message is sent before the answer comes, which is of the turns as
  // they were.
  queryClient.setQueryData(TURNS, [...read(), turn(2, { state: "queued" })]);
  answer({ turns: [turn(1, { ended_at: "x", state: "completed" })] });
  await waitFor(() => expect(result.current.data![0].state).toBe("completed"));
  expect(result.current.data!.map((each) => each.seq)).toEqual([1, 2]);
});

// answered has the API answer the next request that is posted.
const answered = (body: unknown) =>
  vi.spyOn(api, "post").mockResolvedValue(body);

it("keeps a session that it starts as if it had been read", async () => {
  queryClient.setQueryData<SessionPage>(LIST, {
    sessions: [session("s8")],
    more: false,
  });
  const post = answered(session("s9"));
  expect(await startSession("a1", "Fix the cart")).toEqual(session("s9"));
  expect(post).toHaveBeenCalledWith("/v1/agents/a1/sessions", {
    title: "Fix the cart",
  });
  expect(queryClient.getQueryData(["session", "s9"])).toEqual(session("s9"));
  expect(queryClient.getQueryData(["turns", "s9"])).toEqual([]);
  expect(listed()).toEqual([["s9", "s8"], false]);
});

it("adds the turn of a message that it sends to the turns that were read", async () => {
  queryClient.setQueryData(TURNS, [turn(1)]);
  const post = answered(turn(2, { state: "queued" }));
  expect(await sendMessage("s1", "Next")).toEqual(turn(2, { state: "queued" }));
  expect(post).toHaveBeenCalledWith("/v1/sessions/s1/messages", {
    text: "Next",
  });
  expect(seqs()).toEqual([1, 2]);
  // The session may have told of the turn before the answer came.
  await sendMessage("s1", "Next");
  expect(seqs()).toEqual([1, 2]);
});

it("keeps a turn that it stops as the API then has it", async () => {
  const items = [{ type: "message" as const, text: "So far." }];
  queryClient.setQueryData(TURNS, [
    turn(1),
    turn(2, { state: "queued", position: 2, items }),
  ]);
  const [first] = read();
  const post = answered(
    turn(2, { state: "running", cancel_requested_at: "now" }),
  );
  await stopTurn("s1", "t2");
  expect(post).toHaveBeenCalledWith("/v1/turns/t2/cancel");
  // What its events told stays: its record does not have it.
  expect(read()[1]).toEqual(
    turn(2, {
      state: "running",
      cancel_requested_at: "now",
      position: 2,
      items,
    }),
  );
  expect(read()[0]).toBe(first);

  // A turn that has ended is as its record has it.
  const ended = turn(2, { state: "completed", ended_at: "x" });
  queryClient.setQueryData(TURNS, [turn(1), ended]);
  await stopTurn("s1", "t2");
  expect(read()[1]).toEqual(ended);
});

it("takes a turn that ended by itself for stopped", async () => {
  queryClient.setQueryData(TURNS, [turn(2, { state: "running" })]);
  const refusal = { code: "turn_ended", message: "the turn has ended" };
  const post = vi
    .spyOn(api, "post")
    .mockRejectedValueOnce(new ApiError(409, refusal, {}));
  await stopTurn("s1", "t2");
  expect(read()[0].state).toBe("running");
  // Any other refusal is the caller's to show.
  const other = new ApiError(403, { code: "forbidden", message: "no" }, {});
  post.mockRejectedValueOnce(other);
  await expect(stopTurn("s1", "t2")).rejects.toBe(other);
});

it("reads a session and its turns again", async () => {
  const invalidate = vi
    .spyOn(queryClient, "invalidateQueries")
    .mockResolvedValue();
  await rereadSession("s1");
  expect(invalidate).toHaveBeenCalledWith({ queryKey: ["session", "s1"] });
  expect(invalidate).toHaveBeenCalledWith({ queryKey: TURNS });
});
