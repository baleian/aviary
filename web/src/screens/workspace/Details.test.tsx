// @vitest-environment happy-dom
import { cleanup, render } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { afterEach, expect, it, vi } from "vitest";
import type { ProjectContext } from "../../api/project.ts";
import type { Tinted } from "../../api/queries.ts";
import type { Agent, Me, Session, Turn } from "../../api/types.ts";
import { Details } from "./Details.tsx";

afterEach(cleanup);

const agent = {
  id: "a1",
  slug: "helper",
  name: "Helper",
  description: "",
  harness: "codex",
  model: { provider: "pr1", name: "m" },
  tint: 0,
} as Tinted<Agent>;

const context = {
  project: { slug: "shop" },
  providers: [{ id: "pr1", name: "Gateway" }],
  members: [{ user: { id: "u2", username: "bo", display_name: "Bo Ra" } }],
} as unknown as ProjectContext;

const session = (more: Partial<Session> = {}) =>
  ({
    id: "s1",
    agent_id: "a1",
    created_by: "u2",
    created_at: "2026-11-22T10:00:00Z",
    ...more,
  }) as Session;

const turn = (seq: number, state: Turn["state"], usage?: [number, number]) =>
  ({
    id: "t" + seq,
    seq,
    state,
    usage: usage && { input_tokens: usage[0], output_tokens: usage[1] },
  }) as Turn;

// show draws the details, and returns their pane with what each row says.
function show(
  of: {
    agent?: Tinted<Agent>;
    me?: Partial<Me>;
    session?: Session;
    turns?: Turn[];
    empty?: boolean;
  } = {},
) {
  const onClose = vi.fn();
  const { container } = render(
    <MemoryRouter>
      <Details
        context={context}
        agent={of.agent ?? agent}
        me={{ id: "u1", admin: false, ...of.me } as Me}
        session={of.session}
        turns={of.turns}
        empty={of.empty}
        onClose={onClose}
      />
    </MemoryRouter>,
  );
  const pane = container.querySelector(".pane.details")!;
  const rows = new Map<string, string>();
  for (const name of pane.querySelectorAll("dt")) {
    rows.set(name.textContent, name.nextElementSibling!.textContent);
  }
  return { pane, rows, onClose };
}

it("says whose a session is, how it stands and what its turns took", () => {
  const turns = [
    turn(1, "completed", [1200, 30]),
    turn(2, "failed"),
    turn(3, "completed", [800, 45]),
  ];
  const { pane, rows } = show({ session: session(), turns });
  expect(rows.get("Owner")).toBe("BRBo Ra");
  expect(rows.get("Turns")).toBe("32 completed, 1 failed");
  expect(rows.get("Sandbox")).toBe("Not started");
  expect(pane.querySelector(".tok")!.textContent).toBe("2,000in75out");
  expect(pane.querySelector(".r-s h4 small")).toBeNull();
});

it("tells of a session without turns", () => {
  const { pane, rows } = show({ session: session(), turns: [] });
  expect(rows.get("Turns")).toBe("0");
  expect(pane.querySelector(".tok")!.textContent).toBe("0in0out");
});

it("says when a session ended, and whether it expired", () => {
  const ended = show({
    session: session({ ended_at: "2026-11-22T11:00:00Z" }),
  });
  expect(ended.rows.has("Ended")).toBe(true);
  expect(ended.rows.get("Sandbox")).toBe("Deleted");
  cleanup();
  const expired = show({
    session: session({
      ended_at: "2026-11-22T11:00:00Z",
      end_reason: "expired",
    }),
  });
  expect(expired.rows.has("Expired")).toBe(true);
  expect(expired.rows.has("Ended")).toBe(false);
});

it("names no owner without an identity provider", () => {
  const { rows } = show({
    me: { id: undefined, admin: true },
    session: session({ created_by: undefined }),
  });
  expect(rows.has("Owner")).toBe(false);
  expect(rows.has("Started")).toBe(true);
});

it("tells of the agent: what runs it", () => {
  const plain = show();
  expect(plain.rows.get("Provider")).toBe("Gateway");
  expect(plain.rows.has("Approval")).toBe(false);
  expect(plain.pane.querySelector(".about")).toBeNull();
  expect(plain.pane.querySelector("h4 a")!.getAttribute("href")).toBe(
    "/p/shop/agents",
  );
  cleanup();
  const described = show({
    agent: {
      ...agent,
      description: "Helps.",
      model: { provider: "gone", name: "m" },
    },
  });
  expect(described.rows.get("Provider")).toBe("Unknown");
  expect(described.pane.querySelector(".about")!.textContent).toBe("Helps.");
});

it("is empty while a session is read", () => {
  const { pane } = show({ empty: true });
  expect(pane.textContent).toBe("");
});
