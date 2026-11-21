import { afterEach, expect, it, vi } from "vitest";
import { queryClient } from "../query.ts";
import { api } from "./client.ts";
import { olderSessions, olderTurns } from "./conversation.ts";
import type { SessionPage } from "./feed.ts";
import type { Session, Turn } from "./types.ts";

afterEach(() => {
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
const turn = (seq: number) => ({ id: "t" + seq, seq }) as Turn;
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
