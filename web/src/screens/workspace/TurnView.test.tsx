// @vitest-environment happy-dom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";
import * as conversation from "../../api/conversation.ts";
import type { Tinted } from "../../api/queries.ts";
import type { Agent, Item, McpServer, Turn } from "../../api/types.ts";
import * as toasts from "../../ui/toast.ts";
import type { Who } from "../../ui/who.ts";
import { TurnView } from "./TurnView.tsx";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

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
function show(
  of: Partial<Turn>,
  items: Item[] | null = [],
  more: {
    servers?: McpServer[];
    kept?: boolean;
    awaits?: "wake";
    onStop?: () => void;
    onAgain?: (text: string) => void;
    onAnswer?: (request: string, option: string) => Promise<boolean>;
  } = {},
) {
  // Without items the turn is as its record has it before it has ended.
  const drawn = items ? turn({ ...of, items }) : turn(of);
  // The files of a message are read for their pictures.
  const { container } = render(
    <QueryClientProvider client={new QueryClient()}>
      <TurnView turn={drawn} agent={agent} person={person} {...more} />
    </QueryClientProvider>,
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

it("shows the files that the message carries above its text, in its order, and saves one with its button", async () => {
  const attachments = [
    { id: "a2", name: "notes-k.txt", media_type: "text/plain", size: 5 },
    {
      id: "a1",
      name: "결제 웹훅 재시도 설계.pdf",
      media_type: "application/pdf",
      size: 2411724,
    },
    { id: "a3", name: "image.png", media_type: "image/png", size: 96256 },
  ];
  let saved = () => {};
  const save = vi.spyOn(conversation, "saveAttachment").mockReturnValue(
    new Promise((resolve) => {
      saved = resolve;
    }),
  );
  const user = userEvent.setup();
  const { element } = show({ attachments });
  const files = [
    ...element.querySelectorAll<HTMLElement>(".said-u .files .file"),
  ];
  expect(files.map((file) => file.querySelector(".what")!.textContent)).toEqual(
    ["notes-k.txt5 bytes", "결제 웹훅 재시도 설계.pdf2.3 MB", "image.png94 KB"],
  );
  // The tile of a file says what it is.
  expect(files.map((file) => file.querySelector(".tile")!.textContent)).toEqual(
    ["txt", "pdf", "png"],
  );
  // The files come first, as they do where a message is written.
  expect(element.querySelector(".said-u")!.lastElementChild!.className).toBe(
    "bub-u",
  );
  const pdf = within(files[1]).getByRole<HTMLButtonElement>("button", {
    name: "Download 결제 웹훅 재시도 설계.pdf",
  });
  await user.click(pdf);
  expect(save).toHaveBeenCalledWith(attachments[1]);
  // While the file comes, it is not asked for again.
  expect(pdf.disabled).toBe(true);
  expect(pdf.querySelector(".ring")).toBeTruthy();
  await user.click(pdf);
  expect(save).toHaveBeenCalledTimes(1);
  saved();
  await vi.waitFor(() => expect(pdf.disabled).toBe(false));
  expect(pdf.querySelector(".ring")).toBeNull();
  cleanup();

  // A message of files alone has no text.
  const alone = show({ text: "", attachments: attachments.slice(0, 1) });
  expect(alone.element.querySelector(".bub-u")).toBeNull();
  expect(alone.element.querySelectorAll(".files .file")).toHaveLength(1);
  cleanup();

  // A message without files has no list of them.
  expect(show({}).element.querySelector(".files")).toBeNull();
});

it("shows the files of a session that has ended, which are kept no longer, without a way to save them", () => {
  const attachments = [
    { id: "a1", name: "env.example", media_type: "text/plain", size: 612 },
  ];
  const { element } = show({ attachments }, [], { kept: false });
  const file = element.querySelector(".files .file")!;
  expect(file.querySelector(".what")!.textContent).toBe("env.example612 bytes");
  // Nothing can be done with it.
  expect(element.querySelector(".files button")).toBeNull();
});

it("says that a file could not be saved", async () => {
  vi.spyOn(conversation, "saveAttachment").mockRejectedValue(
    new Error("no such resource"),
  );
  const toasted = vi.spyOn(toasts, "oops").mockImplementation(() => {});
  const user = userEvent.setup();
  const { element } = show({
    attachments: [
      { id: "a1", name: "notes.txt", media_type: "text/plain", size: 5 },
    ],
  });
  const file = within(element).getByRole<HTMLButtonElement>("button", {
    name: "Download notes.txt",
  });
  await user.click(file);
  await vi.waitFor(() => expect(toasted).toHaveBeenCalledTimes(1));
  expect(file.disabled).toBe(false);
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
  // A command that succeeded and shows its output need not say its code.
  expect(test.querySelector(".cmd .tag")).toBeNull();
  expect(test.textContent).not.toContain("Exited with code");
  expect(edit.querySelector(".c-fail")).toBeTruthy();
  expect(edit.querySelector(".step-p")!.textContent).toBe("src/a.ts");
  expect(edit.querySelector("span.dl")!.textContent).toBe("- a");
  expect(edit.textContent).toContain("Exited with code 1.");
  // The whole of a call is there to read, as the harness gave it.
  expect(edit.querySelector(".raw pre")!.textContent).toContain('"oldString"');
  // A call of a kind without an icon, which the end of the turn cut short.
  const odd = groups[1].querySelector<HTMLElement>(".step")!;
  expect(odd.querySelector(".step-t")!.textContent).toBe("odd");
  expect(odd.querySelector('[title="Did not finish"]')).toBeTruthy();
  expect(odd.querySelector("use")!.getAttribute("href")).toBe("#i-wrench");
});

it("names a tool of an MCP server apart from its server", () => {
  const servers = [
    {
      id: "m1",
      project_id: "p1",
      slug: "issue-tracker",
      name: "Issue tracker",
      url: "https://tracker.example.com/mcp",
      transport: "http",
      created_at: "2026-11-22T09:00:00Z",
      updated_at: "2026-11-22T09:00:00Z",
    },
  ] satisfies McpServer[];
  const { element } = show(
    {},
    [
      {
        type: "tool_call",
        id: "c1",
        kind: "execute",
        title: "mcp.issue-tracker.search_issues",
        status: "completed",
        input: { command: "search", query: "total" },
        mcp: { server: "m1", slug: "issue-tracker", tool: "search_issues" },
      },
      // A server that the project no longer has goes by its slug.
      {
        type: "tool_call",
        id: "c2",
        kind: "other",
        title: "errors_list_issues",
        status: "completed",
        mcp: { server: "m2", slug: "errors", tool: "list_issues" },
      },
    ],
    { servers },
  );
  const [search, list] = element.querySelectorAll<HTMLElement>(".step");
  expect(search.querySelector(".step-t")!.textContent).toBe("search_issues");
  expect(search.querySelector("summary .tag")!.className).toBe("tag srv");
  expect(search.querySelector("summary .tag")!.textContent).toBe(
    "Issue tracker",
  );
  expect(search.querySelector("use")!.getAttribute("href")).toBe("#i-plug");
  // What a tool of a server is given is not a command of the workspace.
  expect(search.querySelector(".cmd")).toBeNull();
  expect(search.querySelector(".step-f")!.textContent).toContain("search");
  expect(list.querySelector(".step-t")!.textContent).toBe("list_issues");
  expect(list.querySelector("summary .tag")!.textContent).toBe("errors");
});

it("names the MCP servers that the turn ran without", () => {
  const servers = [
    {
      id: "m1",
      project_id: "p1",
      slug: "docs",
      name: "Docs search",
      url: "https://docs.example.com/sse",
      transport: "sse",
      created_at: "2026-11-22T09:00:00Z",
      updated_at: "2026-11-22T09:00:00Z",
    },
  ] satisfies McpServer[];
  const notice = (
    left: { id: string; name: string; transport: "sse" | "http" }[],
  ) =>
    show({}, [{ type: "mcp_unsupported", servers: left }], {
      servers,
    }).element.querySelector(".state")!.textContent;
  expect(notice([{ id: "m1", name: "docs", transport: "sse" }])).toBe(
    "Docs search was left out: the codex harness does not support SSE servers.",
  );
  // A server that the project no longer has goes by its slug.
  expect(
    notice([
      { id: "m1", name: "docs", transport: "sse" },
      { id: "m2", name: "tracker", transport: "http" },
    ]),
  ).toBe(
    "Docs search and tracker were left out: the codex harness does not support SSE or Streamable HTTP servers.",
  );
});

// steps draws tool calls, and returns the card of each.
function steps(...calls: Record<string, unknown>[]) {
  const items = calls.map(
    (call, index) =>
      ({
        type: "tool_call",
        id: "c" + index,
        status: "completed",
        ...call,
      }) as Item,
  );
  return [...show({}, items).element.querySelectorAll<HTMLElement>(".step")];
}

it("says how a command ended where it is shown", () => {
  const [quiet, failed, running] = steps(
    // A harness that tells only the code that a command exited with.
    {
      kind: "execute",
      input: {
        command: "git status",
        cwd: "/home/workspace",
        description: "Shows what has changed",
        timeout: 120000,
      },
      output: { exit_code: 0 },
    },
    {
      kind: "execute",
      status: "failed",
      input: { command: "npm run build" },
      output: { output: "error TS2322", metadata: { exit: 2 } },
    },
    { kind: "execute", status: "in_progress", input: { command: "npm ci" } },
  );
  expect(quiet.querySelector(".cmd")!.textContent).toBe(
    "$git statusin /home/workspaceexit 0",
  );
  expect(quiet.querySelector(".cmd .tag")!.className).toBe("tag");
  // A command says what was run: what else it was given is in the raw input.
  expect(quiet.querySelector(".step-f")).toBeNull();
  expect(quiet.querySelector(".step-b > pre")).toBeNull();
  expect(quiet.textContent).not.toContain("Exited with code");
  expect(failed.querySelector(".cmd .tag")!.textContent).toBe("exit 2");
  expect(failed.querySelector(".cmd .tag")!.className).toBe("tag bad");
  const said = failed.querySelector(".step-b > pre")!;
  expect(said.className).toBe("code bad");
  expect(said.textContent).toBe("error TS2322");
  expect(failed.textContent).not.toContain("The tool call failed");
  // A command that has not ended has no code yet.
  expect(running.querySelector(".cmd")!.textContent).toBe("$npm ci");
});

it("says what a tool call was given, beside its file", () => {
  const [search, fetch, bare] = steps(
    {
      kind: "search",
      title: "grep",
      input: {
        pattern: "retry.*backoff",
        path: "/home/workspace/src",
        include: "*.ts",
        literal: false,
        context: 2,
        // What is not plain text is left to the raw input.
        globs: ["a", "b"],
        options: { deep: true },
        note: "",
      },
      output: { output: "src/sender.ts:41: retry" },
    },
    {
      kind: "fetch",
      title: "webfetch",
      input: { a: 1, b: 2, c: 3, d: 4, e: 5, f: 6, g: 7 },
    },
    { kind: "other", title: "task", status: "failed", input: {} },
  );
  expect(search.querySelector(".step-p")!.textContent).toBe(
    "/home/workspace/src",
  );
  const rows = (card: HTMLElement) =>
    [...card.querySelectorAll(".step-f > div")].map((row) => [
      row.querySelector("dt")!.textContent,
      row.querySelector("dd")!.textContent,
    ]);
  expect(rows(search)).toEqual([
    ["pattern", "retry.*backoff"],
    ["include", "*.ts"],
    ["literal", "false"],
    ["context", "2"],
  ]);
  expect(search.querySelector(".step-b > pre")!.textContent).toBe(
    "src/sender.ts:41: retry",
  );
  // Only the first fields are told of: the rest is in the raw input.
  expect(rows(fetch).map(([name]) => name)).toEqual("abcdef".split(""));
  expect(fetch.querySelector(".raw pre")!.textContent).toContain('"g": 7');
  // A call that failed without a word says only that.
  expect(bare.querySelector(".step-f")).toBeNull();
  expect(bare.querySelector(".step-n")!.textContent).toBe(
    "The tool call failed.",
  );
});

it("shows what a tool call changed, without what its card already says", () => {
  const diff =
    "--- a/NOTES.md\n+++ b/NOTES.md\n@@ -3,3 +3,4 @@\n ## Requests\n+- a note\n--- a rule";
  const lines = (card: HTMLElement) =>
    [...card.querySelectorAll(".code.diff span")].map((line) => [
      line.className,
      line.textContent,
    ]);
  const [named, unnamed, refused] = steps(
    {
      kind: "edit",
      title: "NOTES.md",
      input: { filePath: "/home/workspace/NOTES.md" },
      output: { output: "Edit applied successfully.", metadata: { diff } },
    },
    // A harness that names the file only in the diff.
    {
      kind: "edit",
      title: "Edit NOTES.md",
      input: {
        changes: { "/home/workspace/NOTES.md": { unified_diff: diff } },
      },
    },
    {
      kind: "edit",
      title: "src/a.ts",
      status: "failed",
      input: { filePath: "src/a.ts", oldString: "a", newString: "b" },
      output: { error: "The user rejected permission to use this tool." },
    },
  );
  // The file is named above the change, so the change does not name it
  // again. A line that was taken out is one, though it begins like a name.
  expect(named.querySelector(".step-p")!.textContent).toBe(
    "/home/workspace/NOTES.md",
  );
  expect(lines(named)).toEqual([
    ["hk", "@@ -3,3 +3,4 @@"],
    ["ad", "+- a note"],
    ["dl", "--- a rule"],
  ]);
  // What a tool said of a change that it made is not shown beside it.
  const blocks = [...named.querySelectorAll(".step-b > pre")];
  expect(blocks.map((block) => block.className)).toEqual(["code diff"]);
  expect(unnamed.querySelector(".step-p")).toBeNull();
  expect(lines(unnamed).slice(0, 3)).toEqual([
    ["hk", "--- a/NOTES.md"],
    ["hk", "+++ b/NOTES.md"],
    ["hk", "@@ -3,3 +3,4 @@"],
  ]);
  // A change that was not made says why, below what it would have been.
  expect(lines(refused)).toEqual([
    ["dl", "- a"],
    ["ad", "+ b"],
  ]);
  const why = refused.querySelector(".step-b > pre.bad")!;
  expect(why.textContent).toBe(
    "The user rejected permission to use this tool.",
  );
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
  // The request comes where it was made: after the steps before it.
  const blocks = [...element.querySelector(".ag-c")!.children];
  expect(blocks.map((block) => block.className)).toEqual([
    "ag-n",
    "steps",
    "appr",
  ]);
});

it("describes a request that names its tool call only by its ID", () => {
  const servers = [
    {
      id: "m1",
      project_id: "p1",
      slug: "web",
      name: "Web search",
      url: "https://web.example.com/mcp",
      transport: "http",
      created_at: "2026-11-22T09:00:00Z",
      updated_at: "2026-11-22T09:00:00Z",
    },
  ] satisfies McpServer[];
  const { element } = show(
    { state: "running", ended_at: undefined },
    [
      {
        type: "tool_call",
        id: "c1",
        kind: "execute",
        title: "mcp.web.headers",
        status: "in_progress",
        mcp: { server: "m1", slug: "web", tool: "headers" },
      },
      {
        type: "approval",
        id: "r1",
        tool_call: { id: "c1", kind: "execute" },
        options: [],
      },
    ],
    { servers },
  );
  const card = element.querySelector(".appr")!;
  expect(card.querySelector(".cmd span")!.textContent).toBe("headers");
  expect(card.querySelector(".tag.srv")!.textContent).toBe("Web search");
});

it("shows a request that waits, and who may answer it", async () => {
  const user = userEvent.setup();
  const waits = { state: "running", ended_at: undefined } as const;
  const options = [{ id: "once", name: "Allow once", kind: "allow_once" }];
  const request = {
    id: "r1",
    tool_call: { id: "c1", kind: "execute", input: { command: "git push" } },
    options,
  };
  // The record of a turn that has not ended has no items, and lists the
  // requests that wait.
  const onAnswer = vi.fn(() => Promise.resolve(true));
  const onStop = vi.fn();
  const listed = show({ ...waits, approvals: [request] }, [], {
    onAnswer,
    onStop,
  });
  const card = listed.element.querySelector(".appr")!;
  expect(card.querySelector(".cmd")!.textContent).toBe("$git pushexecute");
  await user.click(card.querySelector(".btn.pri")!);
  expect(onAnswer).toHaveBeenCalledWith("r1", "once");
  cleanup();

  const read = show(waits, [{ type: "approval", ...request }], {
    onAnswer,
    onStop,
  });
  await user.click(read.element.querySelector(".appr .btn.grey")!);
  expect(onStop).toHaveBeenCalledTimes(1);
  cleanup();

  // A caller who may not answer is told whose turn it is.
  const other = show({ ...waits, created_by: "Bo" }, [
    { type: "approval", ...request },
  ]);
  expect(other.element.querySelector(".appr-w")!.textContent).toBe(
    "Waiting for Bo to answer",
  );
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
  const full = show({
    state: "failed",
    started_at: undefined,
    failure: { reason: "attachment_failed", message: "no room" },
  });
  expect(full.element.querySelector(".fail b")!.textContent).toBe(
    "The files did not fit into the sandbox",
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

it("says what a turn waits for before it starts", () => {
  const starts = {
    state: "queued",
    started_at: undefined,
    ended_at: undefined,
  } as const;
  for (const [awaits, said] of [
    [undefined, "Starting…"],
    ["wake", "Waking up…"],
  ] as const) {
    const starting = show(starts, [], { awaits });
    expect(starting.element.querySelector(".state")!.textContent).toBe(said);
    cleanup();
  }
});

it("offers to take a turn from the queue to who may", async () => {
  const user = userEvent.setup();
  const waits = {
    state: "queued",
    started_at: undefined,
    ended_at: undefined,
    position: 2,
  } as const;
  const onStop = vi.fn();
  const may = show(waits, [], { onStop });
  await user.click(may.element.querySelector(".state .btn")!);
  expect(onStop).toHaveBeenCalledTimes(1);
  cleanup();
  expect(show(waits).element.querySelector(".state .btn")).toBeNull();
});

it("offers to send again the message of a turn that did not run to its end", async () => {
  const user = userEvent.setup();
  const onAgain = vi.fn();
  for (const state of ["failed", "expired"] as const) {
    const { element } = show({ state }, [], { onAgain });
    const again = element.querySelector(".fail .acts .btn")!;
    expect(again.textContent).toBe("Send again");
    await user.click(again);
    expect(onAgain).toHaveBeenLastCalledWith("Do it.");
    cleanup();
    // Not where no message can be sent.
    expect(show({ state }).element.querySelector(".acts")).toBeNull();
    cleanup();
  }
  expect(onAgain).toHaveBeenCalledTimes(2);
  for (const state of ["cancelled", "completed"] as const) {
    const { element } = show({ state }, [], { onAgain });
    expect(element.querySelector(".acts")).toBeNull();
    cleanup();
  }
});
