import { beforeAll, expect, it } from "vitest";
import { prefs } from "../lib/prefs.ts";
import { api, authorize, type TurnEvent } from "./client.ts";
import { add, NOTHING, type Said } from "./items.ts";
import type { Turn } from "./types.ts";

type Event = [type: string, data: Record<string, unknown>];

// said returns what a turn has said and done after the events.
const said = (...events: Event[]) =>
  events.reduce<Said>((so, [type, data]) => add(so, type, data), NOTHING).items;

const piece = (text: string, message?: string): Event => [
  "message.delta",
  { text, message_id: message },
];

it("joins the pieces of a message that follow each other", () => {
  expect(said(piece("Hel", "m1"), piece("lo", "m1"))).toEqual([
    { type: "message", text: "Hello" },
  ]);
  expect(said(piece("Hel"), piece("lo"))).toEqual([
    { type: "message", text: "Hello" },
  ]);
});

it("begins a new item with another message, and with a thought", () => {
  expect(
    said(
      piece("One.", "m1"),
      piece("Two.", "m2"),
      ["thought.delta", { text: "Hm.", message_id: "m2" }],
      piece("Three.", "m2"),
    ),
  ).toEqual([
    { type: "message", text: "One." },
    { type: "message", text: "Two." },
    { type: "thought", text: "Hm." },
    { type: "message", text: "Three." },
  ]);
});

it("leaves out a piece without text", () => {
  expect(said(["message.delta", {}], ["thought.delta", { text: "" }])).toEqual(
    [],
  );
});

it("keeps a tool call where it began, with its updates applied", () => {
  const items = said(
    ["tool.call", { id: "c1", title: "bash", kind: "execute", input: {} }],
    piece("Running it.", "m1"),
    ["tool.update", { id: "c1", status: "completed", output: "done" }],
    // The message goes on: the update began no item.
    piece(" Done.", "m1"),
    ["tool.call", { id: "c2", kind: "read" }],
    piece("Next.", "m1"),
  );
  expect(items).toEqual([
    {
      type: "tool_call",
      id: "c1",
      title: "bash",
      kind: "execute",
      input: {},
      status: "completed",
      output: "done",
    },
    { type: "message", text: "Running it. Done." },
    { type: "tool_call", id: "c2", kind: "read" },
    { type: "message", text: "Next." },
  ]);
});

it("keeps the plan where it began, as it last stood", () => {
  const items = said(
    ["plan.update", { entries: [{ content: "Look" }], note: "first" }],
    piece("Looking."),
    ["plan.update", { entries: [{ content: "Look" }, { content: "Fix" }] }],
  );
  expect(items).toEqual([
    { type: "plan", entries: [{ content: "Look" }, { content: "Fix" }] },
    { type: "message", text: "Looking." },
  ]);
});

it("puts how a request for approval ended into the request", () => {
  const options = [{ id: "once", name: "Allow once", kind: "allow_once" }];
  const items = said(
    ["approval.requested", { id: "1", tool_call: { id: "c1" }, options }],
    ["approval.requested", { id: "2", options }],
    ["approval.resolved", { id: "1", outcome: "selected", option_id: "once" }],
  );
  expect(items).toEqual([
    {
      type: "approval",
      id: "1",
      tool_call: { id: "c1" },
      options,
      outcome: "selected",
      option_id: "once",
    },
    { type: "approval", id: "2", options },
  ]);
});

it("tells a tool call from a request with the same ID", () => {
  const items = said(
    ["tool.call", { id: "1", kind: "read" }],
    ["approval.requested", { id: "1", options: [] }],
    ["tool.update", { id: "1", status: "completed" }],
  );
  expect(items).toEqual([
    { type: "tool_call", id: "1", kind: "read", status: "completed" },
    { type: "approval", id: "1", options: [] },
  ]);
});

it("returns the same after an event that is not of an item", () => {
  const before = add(NOTHING, ...piece("Hello"));
  expect(add(before, "turn.started", {})).toBe(before);
  expect(add(before, "session.update", { title: "A title" })).toBe(before);
  expect(add(before, "turn.completed", { stop_reason: "end_turn" })).toBe(
    before,
  );
});

it("changes nothing of what it was given", () => {
  const frozen = (so: Said): Said => {
    for (const item of so.items) Object.freeze(item);
    Object.freeze(so.items);
    return Object.freeze(so);
  };
  let so = NOTHING;
  const events: Event[] = [
    piece("a", "m1"),
    piece("b", "m1"),
    ["tool.call", { id: "c1" }],
    ["tool.update", { id: "c1", status: "completed" }],
    ["plan.update", { entries: [] }],
    ["plan.update", { entries: [{ content: "x" }] }],
    ["approval.requested", { id: "1" }],
    ["approval.resolved", { id: "1", outcome: "cancelled" }],
  ];
  for (const [type, data] of events) so = frozen(add(so, type, data));
  expect(so.items.length).toBe(4);
});

beforeAll(() => {
  prefs.set("mock.speed", "0");
});

it("assembles what the API records of the turn", async () => {
  authorize("alice");
  const { projects } = await api.get<{
    projects: { id: string; slug: string }[];
  }>("/v1/projects");
  const project = projects.find((each) => each.slug === "storefront")!;
  const { agents } = await api.get<{ agents: { id: string; slug: string }[] }>(
    `/v1/projects/${project.id}/agents`,
  );
  for (const slug of ["reviewer", "docs-writer"]) {
    // The two harnesses shape their tool calls differently.
    const agent = agents.find((each) => each.slug === slug)!;
    const session = await api.post<{ id: string }>(
      `/v1/agents/${agent.id}/sessions`,
      { title: "Assembled" },
    );
    // A request that is not a question has the agents of the mock work
    // with their tools.
    const turn = await api.post<Turn>(`/v1/sessions/${session.id}/messages`, {
      text: "Note down that the checkout total rounds twice, and run the tests.",
    });
    const events: TurnEvent[] = [];
    const ended = new Promise<void>((resolve) => {
      api.events(turn.id, {
        onEvent: (event) => {
          events.push(event);
          if (event.type.startsWith("turn.") && event.type !== "turn.started")
            resolve();
        },
      });
    });
    await ended;
    const record = await api.get<Turn>("/v1/turns/" + turn.id);
    expect(record.state).toBe("completed");
    expect(record.items!.length).toBeGreaterThan(4);
    expect(
      said(...events.map((event): Event => [event.type, event.data])),
    ).toEqual(record.items);
  }
});
