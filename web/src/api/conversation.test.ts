// @vitest-environment happy-dom
import { QueryClientProvider } from "@tanstack/react-query";
import { cleanup, renderHook, waitFor } from "@testing-library/react";
import { createElement, type ReactNode } from "react";
import { afterEach, expect, it, vi } from "vitest";
import { queryClient } from "../query.ts";
import { api, ApiError, type TurnEvent } from "./client.ts";
import {
  advance,
  answerRequest,
  endSession,
  followTurn,
  joinTurn,
  noteTurn,
  olderSessions,
  olderTurns,
  renameSession,
  rereadSession,
  saveAttachment,
  sendMessage,
  startSession,
  stopTurn,
  suggestTitle,
  uploadAttachment,
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
function following(onError = vi.fn(), onTitle?: (title: unknown) => void) {
  let tell!: (event: TurnEvent) => void;
  const stop = vi.fn();
  vi.spyOn(api, "events").mockImplementation((_turn, handlers) => {
    tell = handlers.onEvent;
    return stop;
  });
  expect(followTurn("s1", "t2", onError, onTitle)).toBe(stop);
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

it("uploads a file under its name, and sends a message with the files that it carries", async () => {
  const uploaded = {
    id: "a1",
    name: "보고서 (2).pdf",
    media_type: "x",
    size: 5,
  };
  const upload = vi.spyOn(api, "upload").mockResolvedValue(uploaded);
  const file = new File(["to do"], "보고서 (2).pdf");
  const progress = () => {};
  expect(await uploadAttachment("s1", file, progress)).toBe(uploaded);
  expect(upload).toHaveBeenCalledWith(
    "/v1/sessions/s1/attachments?name=%EB%B3%B4%EA%B3%A0%EC%84%9C%20(2).pdf",
    file,
    progress,
  );

  queryClient.setQueryData(TURNS, [turn(1)]);
  const carried = turn(2, { state: "queued", attachments: [uploaded] });
  const post = answered(carried);
  expect(await sendMessage("s1", "", ["a1", "a2"])).toEqual(carried);
  expect(post).toHaveBeenCalledWith("/v1/sessions/s1/messages", {
    text: "",
    attachments: ["a1", "a2"],
  });
  expect(read()[1].attachments).toEqual([uploaded]);
});

it("keeps a turn that it stops as the API then has it", async () => {
  const items = [{ type: "message" as const, text: "So far." }];
  const attachments = [
    { id: "a1", name: "notes.txt", media_type: "text/plain", size: 5 },
  ];
  queryClient.setQueryData(TURNS, [
    turn(1),
    turn(2, { state: "queued", position: 2, items, attachments }),
  ]);
  const [first] = read();
  const post = answered(
    turn(2, { state: "running", cancel_requested_at: "now" }),
  );
  await stopTurn("s1", "t2");
  expect(post).toHaveBeenCalledWith("/v1/turns/t2/cancel");
  // What its events told stays, and so do the files of its message: the
  // answer does not have them.
  expect(read()[1]).toEqual(
    turn(2, {
      state: "running",
      cancel_requested_at: "now",
      position: 2,
      items,
      attachments,
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

it("has the browser save the file of an attachment under its name", async () => {
  const file = new Blob(["to do"], { type: "text/plain" });
  const download = vi.spyOn(api, "download").mockResolvedValue(file);
  const address = vi
    .spyOn(URL, "createObjectURL")
    .mockReturnValue("blob:the-file");
  const revoke = vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => {});
  let saved: [string, string] | undefined;
  vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (
    this: HTMLAnchorElement,
  ) {
    saved = [this.getAttribute("href")!, this.download];
  });
  vi.useFakeTimers();
  try {
    await saveAttachment({
      id: "a1",
      name: "보고서 (2).pdf",
      media_type: "application/pdf",
      size: 5,
    });
    expect(download).toHaveBeenCalledWith("/v1/attachments/a1");
    expect(address).toHaveBeenCalledWith(file);
    expect(saved).toEqual(["blob:the-file", "보고서 (2).pdf"]);
    // The address is given up once the browser has the file.
    expect(revoke).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1000);
    expect(revoke).toHaveBeenCalledWith("blob:the-file");
  } finally {
    vi.useRealTimers();
  }
});

it("reads a session and its turns again", async () => {
  const invalidate = vi
    .spyOn(queryClient, "invalidateQueries")
    .mockResolvedValue();
  await rereadSession("s1");
  expect(invalidate).toHaveBeenCalledWith({ queryKey: ["session", "s1"] });
  expect(invalidate).toHaveBeenCalledWith({ queryKey: TURNS });
});

it("answers a request for approval, unless someone else has", async () => {
  const post = answered({ id: "7", option_id: "once" });
  expect(await answerRequest("t2", "7", "once")).toBe(true);
  expect(post).toHaveBeenCalledWith("/v1/turns/t2/approvals/7", {
    option_id: "once",
  });
  const taken = { code: "approval_answered", message: "already answered" };
  post.mockRejectedValueOnce(new ApiError(409, taken, {}));
  expect(await answerRequest("t2", "7", "once")).toBe(false);
  const ended = new ApiError(409, { code: "turn_ended", message: "over" }, {});
  post.mockRejectedValueOnce(ended);
  await expect(answerRequest("t2", "7", "once")).rejects.toBe(ended);
});

it("passes on the title that a harness suggests for the session", () => {
  queryClient.setQueryData(TURNS, [turn(2, { state: "running" })]);
  const onTitle = vi.fn();
  const tell = following(vi.fn(), onTitle);
  tell("message.delta", { text: "Hello" });
  expect(onTitle).not.toHaveBeenCalled();
  tell("session.update", { title: "A title" });
  expect(onTitle).toHaveBeenCalledWith("A title");
  // Nobody has to listen.
  following()("session.update", { title: "Another" });
});

const titled = (id: string, title: string) =>
  ({ ...session(id), title }) as Session;
const title = (id: string) =>
  queryClient.getQueryData<Session>(["session", id])!.title;

it("gives a session the title that a person wrote", async () => {
  queryClient.setQueryData(["session", "s9"], titled("s9", "Before"));
  queryClient.setQueryData<SessionPage>(LIST, {
    sessions: [titled("s9", "Before")],
    more: false,
  });
  const patch = vi.spyOn(api, "patch").mockResolvedValue(titled("s9", "After"));
  await renameSession("s9", "After");
  expect(patch).toHaveBeenCalledWith("/v1/sessions/s9", { title: "After" });
  expect(title("s9")).toBe("After");
  const page = queryClient.getQueryData<SessionPage>(LIST)!;
  expect(page.sessions[0].title).toBe("After");
});

it("takes the title that a harness suggests only in the place of none, or of its own", async () => {
  const patch = vi
    .spyOn(api, "patch")
    .mockImplementation((path, body) =>
      Promise.resolve(titled(path.split("/").pop()!, (body as Session).title)),
    );
  // A session without a title takes it, cut to a length.
  queryClient.setQueryData(["session", "s1"], titled("s1", ""));
  await suggestTitle("s1", "word ".repeat(40));
  expect(title("s1").length).toBe(120);
  expect(title("s1").endsWith("…")).toBe(true);
  // And another after it: the first was not a person's.
  await suggestTitle("s1", "Second thoughts");
  expect(title("s1")).toBe("Second thoughts");

  // A session that the console named after its first message takes it.
  vi.spyOn(api, "post").mockResolvedValue(titled("s2", "Fix the cart"));
  await startSession("a1", "Fix the cart");
  await suggestTitle("s2", "Cart totals");
  expect(title("s2")).toBe("Cart totals");

  // A title that was there is someone's, and stays.
  queryClient.setQueryData(["session", "s3"], titled("s3", "Named elsewhere"));
  await suggestTitle("s3", "Suggested");
  expect(title("s3")).toBe("Named elsewhere");

  // So does what a person wrote here, even nothing.
  await renameSession("s1", "");
  expect(title("s1")).toBe("");
  patch.mockClear();
  await suggestTitle("s1", "Suggested");
  await suggestTitle("s2", "  ");
  await suggestTitle("s2", 7);
  await suggestTitle("unread", "Suggested");
  expect(patch).not.toHaveBeenCalled();
  expect(title("s1")).toBe("");
});

it("keeps a session that it ends as the API then has it", async () => {
  queryClient.setQueryData(["session", "s9"], session("s9"));
  const ended = { ...session("s9"), ended_at: "2026-11-22T11:00:00Z" };
  const remove = vi.spyOn(api, "delete").mockResolvedValue(ended);
  await endSession("s9");
  expect(remove).toHaveBeenCalledWith("/v1/sessions/s9");
  expect(queryClient.getQueryData(["session", "s9"])).toEqual(ended);
});

it("takes from its session that a turn has left the queue, and was told to stop", async () => {
  const items = [{ type: "message" as const, text: "So far." }];
  const ended = turn(1, { state: "completed", ended_at: "x" });
  queryClient.setQueryData(TURNS, [
    ended,
    turn(2, { state: "queued", position: 1, items }),
  ]);
  // The turn was taken from the queue: its harness does not have the
  // message yet.
  await noteTurn("s1", turn(2, { state: "running" }));
  expect(read()[1]).toEqual(turn(2, { state: "running", position: 1, items }));
  await noteTurn(
    "s1",
    turn(2, { state: "running", started_at: "s", cancel_requested_at: "c" }),
  );
  expect(read()[1]).toEqual(
    turn(2, {
      state: "running",
      started_at: "s",
      cancel_requested_at: "c",
      position: 1,
      items,
    }),
  );

  // A turn only moves on: what its events told is newer than what its
  // session says after them.
  await noteTurn("s1", turn(2, { state: "queued", started_at: "later" }));
  expect(read()[1]).toMatchObject({ state: "running", started_at: "s" });

  // A turn that has ended, and one that was not read, are left alone.
  const before = read();
  await noteTurn("s1", turn(1, { state: "running" }));
  await noteTurn("s1", turn(9, { state: "running" }));
  expect(read()).toBe(before);
  expect(read()[0]).toBe(ended);
});

it("reads the record of a turn that its session says has ended, once", async () => {
  queryClient.setQueryData(TURNS, [turn(2, { state: "running" })]);
  const answer = answering();
  const noted = noteTurn(
    "s1",
    turn(2, { state: "completed", ended_at: "2026-11-22T10:00:09Z" }),
  );
  expect(api.get).toHaveBeenCalledWith("/v1/turns/t2");
  // The events of the turn say the same meanwhile.
  following()("turn.completed", { stop_reason: "end_turn" });
  expect(api.get).toHaveBeenCalledTimes(1);
  const record = turn(2, {
    state: "completed",
    ended_at: "2026-11-22T10:00:09Z",
    items: [{ type: "message", text: "From the record." }],
  });
  answer(record);
  await noted;
  expect(read()[0]).toEqual(record);
  // A turn that ends later is read again.
  queryClient.setQueryData(TURNS, [turn(2, { state: "running" })]);
  const again = answering();
  const later = noteTurn("s1", turn(2, { state: "failed", ended_at: "x" }));
  again(record);
  await later;
  expect(read()[0]).toEqual(record);
});
