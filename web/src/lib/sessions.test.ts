import { describe, expect, it } from "vitest";
import type { Session, Turn } from "../api/types.ts";
import { followed, standing, standingText, status } from "./sessions.ts";

const turn = (more: Partial<Turn>): Turn => ({
  id: "t",
  session_id: "s",
  seq: 1,
  state: "running",
  created_at: "2026-11-22T10:00:00Z",
  ...more,
});

const session = (more: Partial<Session>): Session => ({
  id: "s",
  agent_id: "a",
  title: "",
  created_at: "2026-11-22T10:00:00Z",
  ...more,
});

const asks = [{ id: "1", options: [] }];

describe("status", () => {
  it("is the state of a turn that does not run", () => {
    expect(status(turn({ state: "queued" }))).toBe("queued");
    expect(status(turn({ state: "failed", ended_at: "x" }))).toBe("failed");
  });

  it("tells the moments of a running turn apart", () => {
    expect(status(turn({}))).toBe("starting");
    expect(status(turn({ started_at: "x" }))).toBe("running");
    expect(status(turn({ started_at: "x", approvals: asks }))).toBe("waiting");
    expect(
      status(
        turn({ started_at: "x", approvals: asks, cancel_requested_at: "x" }),
      ),
    ).toBe("stopping");
  });
});

describe("followed", () => {
  it("takes a queued turn without a place in the queue for one that starts", () => {
    expect(followed(turn({ state: "queued" }))).toBe("starting");
    expect(followed(turn({ state: "queued", position: 2 }))).toBe("queued");
    expect(followed(turn({ started_at: "x" }))).toBe("running");
    expect(followed(turn({ state: "expired", ended_at: "x" }))).toBe("expired");
  });
});

describe("standing", () => {
  it("is that of the newest turn while it has not ended", () => {
    const waits = session({ last_turn: turn({ approvals: asks }) });
    expect(standing(waits)).toBe("waiting");
    // Also in a session that is being ended.
    expect(standing({ ...waits, ended_at: "x" })).toBe("waiting");
  });

  it("is how the newest turn ended", () => {
    const done = turn({ state: "completed", ended_at: "x" });
    expect(standing(session({ last_turn: done }))).toBe("completed");
    expect(standingText(session({ last_turn: done }))).toBe("Completed");
  });

  it("says that a session has ended, and whether it expired", () => {
    const done = turn({ state: "completed", ended_at: "x" });
    const ended = session({ last_turn: done, ended_at: "x" });
    expect(standing(ended)).toBe("ended");
    expect(standingText(ended)).toBe("Ended");
    expect(standingText({ ...ended, end_reason: "expired" })).toBe("Expired");
  });

  it("says that a session has no messages yet", () => {
    expect(standing(session({}))).toBe("none");
    expect(standingText(session({}))).toBe("No messages yet");
  });
});
