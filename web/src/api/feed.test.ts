import { afterEach, expect, it, vi } from "vitest";
import { api } from "./client.ts";
import { readSession, readSessions, take } from "./feed.ts";
import type { Session } from "./types.ts";

const session = (id: string, title: string, agent = "a1"): Session => ({
  id,
  agent_id: agent,
  title,
  created_at: "2026-11-22T10:00:00Z",
});

afterEach(() => {
  vi.restoreAllMocks();
});

// reading starts a read of sessions, and returns how to have the API answer
// it.
function reading(count: number, before?: string) {
  let answer!: (sessions: Session[]) => void;
  const get = vi.spyOn(api, "get").mockReturnValue(
    new Promise((resolve) => {
      answer = (sessions) => resolve({ sessions });
    }),
  );
  return { page: readSessions("a1", count, before), answer, get };
}

const ids = (sessions: Session[]) => sessions.map((each) => each.id);

it("returns a session that changed during the read as the stream told it", async () => {
  take(session("s2", "told before the read"));
  const { page, answer } = reading(6);
  take(session("s3", "told during the read"));
  answer([session("s3", "read"), session("s2", "read"), session("s1", "read")]);
  expect((await page).sessions.map((each) => each.title)).toEqual([
    "told during the read",
    // What was told before the read began is older than what was read.
    "read",
    "read",
  ]);
});

it("returns a session that changed while it was read as the stream told it", async () => {
  take(session("s2", "told before the read"));
  for (const told of [false, true]) {
    let answer!: (read: Session) => void;
    const get = vi.spyOn(api, "get").mockReturnValue(
      new Promise((resolve) => {
        answer = resolve;
      }),
    );
    const read = readSession("s2");
    expect(get).toHaveBeenCalledWith("/v1/sessions/s2");
    if (told) take(session("s2", "told during the read"));
    take(session("s3", "another session"));
    answer(session("s2", "read"));
    expect((await read).title).toBe(told ? "told during the read" : "read");
  }
});

it("adds the sessions that were created during the read of the newest", async () => {
  const { page, answer } = reading(6);
  take(session("s8", "created during the read"));
  take(session("s9", "created after that"));
  take(session("s7", "of another agent", "a2"));
  answer([session("s5", "read"), session("s4", "read")]);
  expect(ids((await page).sessions)).toEqual(["s9", "s8", "s5", "s4"]);
});

it("adds nothing to the sessions before another", async () => {
  const { page, answer, get } = reading(6, "s4");
  take(session("s9", "created during the read"));
  answer([session("s3", "read")]);
  expect(ids((await page).sessions)).toEqual(["s3"]);
  expect(get).toHaveBeenCalledWith("/v1/agents/a1/sessions?limit=7&before=s4");
});

it("asks for one session more, which tells whether there are older ones", async () => {
  const read = [session("s5", "read"), session("s4", "read")];
  let reader = reading(2);
  reader.answer([...read, session("s3", "read")]);
  let page = await reader.page;
  expect(reader.get).toHaveBeenCalledWith("/v1/agents/a1/sessions?limit=3");
  expect(ids(page.sessions)).toEqual(["s5", "s4"]);
  expect(page.more).toBe(true);

  reader = reading(2);
  reader.answer(read);
  page = await reader.page;
  expect(ids(page.sessions)).toEqual(["s5", "s4"]);
  expect(page.more).toBe(false);

  // A session that was created meanwhile leaves an older one to be read.
  reader = reading(2);
  take(session("s9", "created during the read"));
  reader.answer(read);
  page = await reader.page;
  expect(ids(page.sessions)).toEqual(["s9", "s5"]);
  expect(page.more).toBe(true);
});

it("asks for no more sessions than the API lists at once", async () => {
  const reader = reading(500);
  reader.answer(
    Array.from({ length: 100 }, (_, index) =>
      session("s" + (999 - index), "read"),
    ),
  );
  const page = await reader.page;
  expect(reader.get).toHaveBeenCalledWith("/v1/agents/a1/sessions?limit=100");
  expect(page.sessions.length).toBe(99);
  expect(page.more).toBe(true);
});
