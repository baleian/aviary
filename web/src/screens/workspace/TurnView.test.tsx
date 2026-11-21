// @vitest-environment happy-dom
import { cleanup, render } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import type { Tinted } from "../../api/queries.ts";
import type { Agent, Item, Turn } from "../../api/types.ts";
import type { Who } from "../../ui/who.ts";
import { TurnView } from "./TurnView.tsx";

afterEach(cleanup);

const agent: Tinted<Agent> = {
  id: "a1",
  project_id: "p1",
  slug: "helper",
  name: "Helper",
  description: "",
  harness: "codex",
  model: { provider: "pr1", name: "m" },
  created_at: "2026-11-22T09:00:00Z",
  updated_at: "2026-11-22T09:00:00Z",
  tint: 0,
};

const person = (id?: string): Who => ({ name: id ?? "Nobody", known: !!id });

const turn = (more: Partial<Turn>): Turn => ({
  id: "t1",
  session_id: "s1",
  seq: 3,
  state: "completed",
  text: "Do it.",
  created_by: "Alice",
  created_at: "2026-11-22T10:00:00Z",
  started_at: "2026-11-22T10:00:02Z",
  ended_at: "2026-11-22T10:01:09Z",
  ...more,
});

// show draws a turn, and returns its text and its element.
function show(of: Partial<Turn>, items: Item[] = []) {
  const { container } = render(
    <TurnView turn={turn({ ...of, items })} agent={agent} person={person} />,
  );
  const element = container.querySelector<HTMLElement>(".turn")!;
  return { element, text: element.textContent ?? "" };
}

it("shows the message, who sent it, and how long the turn took", () => {
  const { element, text } = show({});
  expect(element.id).toBe("turn-3");
  expect(element.querySelector(".turn-h")!.textContent).toMatch(
    /^Turn 3Completed.* · 1m 07s$/,
  );
  expect(element.querySelector(".bub-u")!.textContent).toBe("Do it.");
  expect(element.querySelector(".msg-u .av")!.getAttribute("title")).toBe(
    "Alice",
  );
  expect(text).toContain("Helper");
});

it("shows what the agent said, thought and planned, in order", () => {
  const { element } = show({}, [
    { type: "thought", text: "Let me look." },
    {
      type: "plan",
      entries: [
        { content: "Read", status: "completed" },
        { content: "Write", status: "in_progress", priority: "high" },
        { content: "Test", status: "pending" },
      ],
    },
    { type: "message", text: "Done with **it**." },
  ]);
  const blocks = [...element.querySelector(".ag-c")!.children].map(
    (child) => child.className,
  );
  expect(blocks.slice(1, 4)).toEqual(["th", "plan", "bub-a"]);
  expect(element.querySelector(".plan-h small")!.textContent).toBe(
    "1 of 3 done",
  );
  expect(element.querySelector(".plan li.in_progress")!.textContent).toBe(
    "Writehigh priority",
  );
  // A step that was under way when the turn ended is no longer.
  expect(element.querySelector(".plan .ring")).toBeNull();
  expect(element.querySelector(".bub-a strong")!.textContent).toBe("it");
});

it("groups the tool calls that follow each other, with what came of each", () => {
  const { element } = show({}, [
    {
      type: "tool_call",
      id: "c1",
      kind: "execute",
      title: "npm test",
      status: "completed",
      input: { command: "npm test", cwd: "/home/workspace" },
      output: { stdout: "3 passed", exit_code: 0 },
    },
    {
      type: "tool_call",
      id: "c2",
      kind: "edit",
      title: "src/a.ts",
      status: "failed",
      input: { filePath: "src/a.ts", oldString: "a", newString: "b" },
      output: { exit_code: 1 },
    },
    { type: "message", text: "One failed." },
    { type: "tool_call", id: "c3", kind: "odd", status: "in_progress" },
  ]);
  const groups = element.querySelectorAll(".steps");
  expect(groups.length).toBe(2);
  const [test, edit] = groups[0].querySelectorAll<HTMLElement>(".step");
  expect(test.querySelector(".step-t")!.className).toBe("step-t cmdline");
  expect(test.querySelector(".c-ok")).toBeTruthy();
  expect(test.querySelector(".cmd")!.textContent).toBe(
    "$npm testin /home/workspace",
  );
  expect(test.querySelector(".step-b > pre.code")!.textContent).toBe(
    "3 passed",
  );
  expect(test.textContent).not.toContain("Exited with code");
  expect(edit.querySelector(".c-fail")).toBeTruthy();
  expect(edit.querySelector(".sub.mono")!.textContent).toBe("src/a.ts");
  expect(edit.querySelector("span.dl")!.textContent).toBe("- a");
  expect(edit.textContent).toContain("Exited with code 1.");
  // The whole of a call is there to read, as the harness gave it.
  expect(edit.querySelector(".raw pre")!.textContent).toContain('"oldString"');
  // A call of a kind without an icon, which the end of the turn cut short.
  const odd = groups[1].querySelector<HTMLElement>(".step")!;
  expect(odd.querySelector(".step-t")!.textContent).toBe("odd");
  expect(odd.querySelector('[title="Did not finish"]')).toBeTruthy();
  expect(odd.querySelector("use")!.getAttribute("href")).toBe("#i-cube");
});

it("says how a request for approval ended, with its tool call or alone", () => {
  const options = [
    { id: "o1", name: "Allow once", kind: "allow_once" },
    { id: "o2", name: "Reject", kind: "reject_once" },
  ];
  const { element } = show({}, [
    {
      type: "approval",
      id: "r1",
      tool_call: { id: "c1", title: "git push" },
      options,
      outcome: "selected",
      option_id: "o1",
      answered_by: "Bob",
    },
    { type: "tool_call", id: "c1", kind: "execute", status: "completed" },
    {
      type: "approval",
      id: "r2",
      tool_call: { id: "gone", title: "rm -r build" },
      options,
      outcome: "selected",
      option_id: "o2",
    },
    { type: "approval", id: "r3", options, outcome: "cancelled" },
  ]);
  const step = element.querySelector(".step")!;
  expect(step.querySelector(".kd.y use")!.getAttribute("href")).toBe(
    "#i-shield",
  );
  expect(step.querySelector(".answer")!.textContent).toBe("Allow once by Bob");
  const alone = element.querySelectorAll(".answer.alone");
  expect(alone[0].textContent).toBe("rm -r build: Reject");
  expect(alone[0].className).toBe("answer no alone");
  expect(alone[1].textContent).toBe("Not answered");
});

it("leaves a tool call that waits for approval to its request", () => {
  const { element } = show({ state: "running", ended_at: undefined }, [
    { type: "tool_call", id: "c1", kind: "execute", title: "git push" },
    { type: "tool_call", id: "c2", kind: "read", status: "completed" },
    { type: "approval", id: "r1", tool_call: { id: "c1" }, options: [] },
  ]);
  const steps = element.querySelectorAll(".step");
  expect(steps.length).toBe(1);
  expect(steps[0].querySelector(".tag")!.textContent).toBe("read");
});

it("says how many tokens a turn took, and why the model stopped", () => {
  const usage = {
    input_tokens: 12034,
    output_tokens: 560,
    thought_tokens: 77,
    cached_read_tokens: 9000,
  };
  const done = show({ usage, stop_reason: "max_tokens" });
  const line = done.element.querySelector(".done-l")!;
  expect(line.textContent).toBe(
    "Stopped at the output limit of the model.·12,034 tokens in · 560 out",
  );
  expect(line.querySelector("[title]")!.getAttribute("title")).toBe(
    "77 thought, 9,000 read from cache",
  );
  cleanup();
  expect(show({ usage, stop_reason: "end_turn" }).text).toMatch(
    /12,034 tokens in · 560 out$/,
  );
});

it("says why a turn failed, expired or was stopped", () => {
  const failed = show({
    state: "failed",
    failure: { reason: "sandbox_lost", message: "pod deleted" },
  });
  expect(failed.element.querySelector(".fail")!.textContent).toBe(
    "The sandbox was lost, with the files in itpod deleted",
  );
  cleanup();
  const unknown = show({ state: "failed", failure: { reason: "new_reason" } });
  expect(unknown.element.querySelector(".fail b")!.textContent).toBe(
    "The turn failed",
  );
  cleanup();
  expect(
    show({ state: "expired", started_at: undefined }).element.querySelector(
      ".fail.soft",
    )!.textContent,
  ).toBe("This turn waited too long in the queue");
  cleanup();
  expect(show({ state: "cancelled" }).text).toContain("Stopped");
  cleanup();
  expect(show({ state: "cancelled", started_at: undefined }).text).toContain(
    "Cancelled before it started",
  );
});

it("says what a turn that has not ended is doing", () => {
  const running = { state: "running", ended_at: undefined } as const;
  const waits = { ...running, state: "queued", started_at: undefined } as const;
  const queued = show({ ...waits, position: 1 });
  expect(queued.text).toContain("is in the queue");
  expect(queued.element.querySelector(".chip")!.textContent).toBe("Queued · 1");
  expect(queued.element.querySelector(".state")!.textContent).toBe(
    "In the queue, next to run",
  );
  cleanup();
  const third = show({ ...waits, position: 3 });
  expect(third.element.querySelector(".chip")!.textContent).toBe("Queued · 3");
  expect(third.element.querySelector(".state")!.textContent).toBe(
    "In the queue, 2 ahead",
  );
  cleanup();
  // A turn without a place in the queue was taken, and waits for its
  // sandbox.
  for (const taken of [waits, { ...running, started_at: undefined }]) {
    const starting = show(taken);
    expect(starting.text).toContain("is starting");
    expect(starting.element.querySelector(".chip")!.textContent).toBe(
      "Starting",
    );
    expect(starting.element.querySelector(".state")!.textContent).toBe(
      "Starting…",
    );
    cleanup();
  }
  const working = show(running);
  expect(working.element.querySelector(".turn-h")!.textContent).toContain(
    "started ",
  );
  expect(working.element.querySelector(".state")!.textContent).toBe("Working…");
  cleanup();
  // Once the agent has said something, that shows instead, marked as
  // being written while it is the last.
  const said = show(running, [{ type: "message", text: "Looking." }]);
  expect(said.element.querySelector(".state")).toBeNull();
  expect(said.element.querySelector(".bub-a")!.className).toBe("bub-a typing");
  cleanup();
  const doing = show(running, [
    { type: "message", text: "Looking." },
    { type: "tool_call", id: "c1", kind: "read", status: "in_progress" },
  ]);
  expect(doing.element.querySelector(".bub-a")!.className).toBe("bub-a");
  cleanup();
  const done = show({}, [{ type: "message", text: "Looked." }]);
  expect(done.element.querySelector(".bub-a")!.className).toBe("bub-a");
  cleanup();
  const stopping = show({ ...running, cancel_requested_at: "x" });
  expect(stopping.element.querySelector(".state")!.textContent).toBe(
    "Stopping…",
  );
  cleanup();
  const waiting = show({ ...running, approvals: [{ id: "r1", options: [] }] });
  expect(waiting.text).toContain("is waiting for an answer");
  expect(waiting.element.querySelector(".chip")!.textContent).toBe(
    "Needs approval",
  );
});
