import { afterEach, expect, it, vi } from "vitest";
import { api } from "./client.ts";
import { readSessions, take } from "./feed.ts";
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

// reading starts a read of a page of sessions, and returns how to have the
// API answer it.
function reading(query: string) {
  let answer!: (sessions: Session[]) => void;
  vi.spyOn(api, "get").mockReturnValue(
    new Promise((resolve) => {
      answer = (sessions) => resolve({ sessions });
    }),
  );
  return { page: readSessions("a1", query), answer };
}

it("returns a session that changed during the read as the stream told it", async () => {
  take(session("s2", "told before the read"));
  const { page, answer } = reading("limit=6");
  take(session("s3", "told during the read"));
  answer([session("s3", "read"), session("s2", "read"), session("s1", "read")]);
  expect((await page).map((each) => each.title)).toEqual([
    "told during the read",
    // What was told before the read began is older than what was read.
    "read",
    "read",
  ]);
});

it("adds the sessions that were created during the read of the first page", async () => {
  const { page, answer } = reading("limit=6");
  take(session("s8", "created during the read"));
  take(session("s9", "created after that"));
  take(session("s7", "of another agent", "a2"));
  answer([session("s5", "read"), session("s4", "read")]);
  expect((await page).map((each) => each.id)).toEqual(["s9", "s8", "s5", "s4"]);
});

it("adds nothing to a later page", async () => {
  const { page, answer } = reading("limit=6&before=s4");
  take(session("s9", "created during the read"));
  answer([session("s3", "read")]);
  expect((await page).map((each) => each.id)).toEqual(["s3"]);
});
