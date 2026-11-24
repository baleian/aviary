// @vitest-environment happy-dom
import { cleanup, render } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import type { Session } from "../../api/types.ts";
import { SandboxBadge, SandboxMark } from "./marks.tsx";

afterEach(cleanup);

const session = (more: Partial<Session>): Session => ({
  id: "s1",
  agent_id: "a1",
  title: "",
  created_at: "2026-11-22T10:00:00Z",
  ...more,
});

// show draws the badge and the mark of the session.
function show(of: Session) {
  const { container } = render(
    <>
      <SandboxBadge session={of} always />
      <SandboxMark session={of} />
    </>,
  );
  return {
    badge: container.querySelector(".sb")!,
    mark: container.querySelector(".sbm"),
  };
}

it("says how the sandbox of a session is, in a word and as a mark", () => {
  for (const [sandbox, word, glyph] of [
    ["awake", "Awake", undefined],
    ["asleep", "Asleep", "#i-moon"],
    ["archived", "Archived", "#i-box"],
    ["restoring", "Restoring", "#i-box"],
    ["lost", "Lost", "#i-warn"],
  ] as const) {
    const { badge, mark } = show(session({ sandbox }));
    expect(badge.textContent).toBe(word);
    expect(mark!.getAttribute("aria-label")).toBe(
      "Sandbox " + word.toLowerCase(),
    );
    for (const drawn of [badge, mark!]) {
      expect(drawn.querySelector("use")?.getAttribute("href")).toBe(glyph);
    }
    cleanup();
  }
});

it("says of a session without a sandbox that it has none, and marks nothing", () => {
  const fresh = show(session({}));
  expect(fresh.badge.textContent).toBe("Not started");
  expect(fresh.mark).toBeNull();
  cleanup();
  const over = show(
    session({ ended_at: "2026-11-22T11:00:00Z", sandbox: "asleep" }),
  );
  expect(over.badge.textContent).toBe("Deleted");
  expect(over.mark).toBeNull();
});

it("says what the API says of a sandbox that it does not know, and marks nothing", () => {
  const strange = show(session({ sandbox: "moving" as Session["sandbox"] }));
  expect(strange.badge.textContent).toBe("moving");
  expect(strange.mark).toBeNull();
});
