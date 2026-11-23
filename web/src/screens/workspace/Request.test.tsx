// @vitest-environment happy-dom
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";
import type { Item } from "../../api/types.ts";
import { Request } from "./Request.tsx";

afterEach(cleanup);

type ApprovalItem = Extract<Item, { type: "approval" }>;

const options = [
  { id: "once", name: "Allow once", kind: "allow_once" },
  { id: "always", name: "Always allow", kind: "allow_always" },
  { id: "reject", name: "Reject", kind: "reject_once" },
];

// show draws a request for what a tool call is about to do, and returns its
// card with what it was given to call.
function show(
  call: ApprovalItem["tool_call"],
  more: Partial<ApprovalItem> = {},
  may = true,
) {
  const onAnswer = vi.fn(() => Promise.resolve(true));
  const onStop = vi.fn();
  render(
    <Request
      item={{ type: "approval", id: "7", tool_call: call, options, ...more }}
      asked={{ name: "Bo Ra", known: true }}
      onAnswer={may ? onAnswer : undefined}
      onStop={may ? onStop : undefined}
    />,
  );
  const card = screen.getByRole("group", { name: "Request for approval" });
  return { card, onAnswer, onStop };
}

it("asks to run a command, and shows the command", () => {
  const { card } = show({
    id: "c1",
    kind: "execute",
    title: "git push",
    input: { command: "git push origin main", cwd: "/home/workspace" },
  });
  expect(card.querySelector("h3")!.textContent).toBe("May I run this command?");
  expect(card.querySelector(".cmd")!.textContent).toBe(
    "$git push origin mainexecute",
  );
  expect(card.querySelector(".code")).toBeNull();
});

it("asks for a tool of an MCP server by the tool and the server", () => {
  render(
    <Request
      item={{
        type: "approval",
        id: "7",
        tool_call: {
          id: "c1",
          kind: "execute",
          title: "mcp.ci.run_job",
          // What a tool of a server is given is not a command of the
          // workspace, nor a file of it.
          input: { command: "deploy", path: "jobs/deploy" },
          mcp: { server: "m1", slug: "ci", tool: "run_job" },
        },
        options,
      }}
      server="Build server"
      asked={{ name: "Bo Ra", known: true }}
    />,
  );
  const card = screen.getByRole("group", { name: "Request for approval" });
  expect(card.querySelector("h3")!.textContent).toBe("May I do this?");
  expect(card.querySelector(".cmd")!.textContent).toBe("›run_jobBuild server");
  expect(card.querySelector(".cmd .tag")!.className).toBe("tag srv");
  expect(card.querySelector(".code")!.textContent).toContain('"deploy"');
});

it("says when a command reaches outside the workspace, and where", () => {
  // OpenCode asks for the folders before it asks for the command.
  const { card } = show({
    id: "c1",
    kind: "other",
    title: "touch /tmp/note",
    input: {
      command: "touch /tmp/note",
      directories: ["/tmp", "/var/log"],
      patterns: ["/tmp/*"],
    },
  });
  expect(card.querySelector("h3")!.textContent).toBe(
    "May I run this command outside the workspace?",
  );
  expect(card.querySelector(".cmd")!.textContent).toBe("$touch /tmp/noteother");
  expect(card.querySelector("p.mono")!.textContent).toBe("/tmp, /var/log");
  cleanup();
  const inside = show({
    id: "c1",
    kind: "execute",
    input: { command: "ls", directories: [] },
  });
  expect(inside.card.querySelector("h3")!.textContent).toBe(
    "May I run this command?",
  );
  expect(inside.card.querySelector("p.mono")).toBeNull();
});

it("asks to change a file, and shows the change", () => {
  const { card } = show({
    id: "c1",
    kind: "edit",
    title: "NOTES.md",
    input: {
      filePath: "/home/workspace/NOTES.md",
      oldString: "old",
      newString: "new",
    },
  });
  expect(card.querySelector("h3")!.textContent).toBe("May I change this file?");
  expect(card.querySelector(".cmd")!.textContent).toBe(
    "›/home/workspace/NOTES.mdedit",
  );
  expect(card.querySelector(".code .dl")!.textContent).toBe("- old");
  expect(card.querySelector(".code .ad")!.textContent).toBe("+ new");
  // The card names the file, and the change is only the lines.
  expect(card.querySelector(".code .hk")).toBeNull();
});

it("asks to do anything else, with what the tool is given", () => {
  const input = { url: "https://example.com/docs" };
  const fetching = show({ id: "c1", kind: "fetch", title: "Read docs", input });
  expect(fetching.card.querySelector("h3")!.textContent).toBe("May I do this?");
  expect(fetching.card.querySelector(".cmd")!.textContent).toBe(
    "›Read docsfetch",
  );
  expect(fetching.card.querySelector(".code")!.textContent).toBe(
    JSON.stringify(input, null, 2),
  );
  cleanup();
  // A request that says nothing of its tool call still asks.
  const bare = show(undefined);
  expect(bare.card.querySelector(".cmd")!.textContent).toBe("›tool");
  expect(bare.card.querySelector(".code")).toBeNull();
  cleanup();
  // The file that the tool call is about says enough.
  const reading = show({
    id: "c1",
    kind: "read",
    input: { filePath: "/etc/hosts" },
  });
  expect(reading.card.querySelector(".cmd")!.textContent).toBe(
    "›/etc/hostsread",
  );
  expect(reading.card.querySelector(".code")).toBeNull();
});

it("offers the answers of the request, each as its kind", () => {
  const more = [...options, { id: "other", name: "Later", kind: "defer" }];
  const { card } = show({ id: "c1", kind: "execute" }, { options: more });
  const buttons = [...card.querySelectorAll(".appr-a .btn")];
  expect(
    buttons.map((button) => [button.textContent, button.className]),
  ).toEqual([
    ["Allow once", "btn pri"],
    ["Always allow", "btn sec"],
    ["Reject", "btn dq"],
    ["Later", "btn sec"],
    ["Stop turn", "btn grey"],
  ]);
  expect(card.querySelector(".appr-a")!.className).toBe("appr-a");
  cleanup();
  // A long answer has a line of its own, and so have the others.
  const long = {
    id: "always",
    name: "Yes, and do not ask again for this",
    kind: "allow_always",
  };
  const tall = show({ id: "c1" }, { options: [options[0], long] });
  expect(tall.card.querySelector(".appr-a")!.className).toBe("appr-a tall");
});

it("sends the answer that is chosen, and waits until the harness has it", async () => {
  const user = userEvent.setup();
  const { card, onAnswer } = show({ id: "c1", kind: "execute" });
  await user.click(screen.getByRole("button", { name: "Always allow" }));
  expect(onAnswer).toHaveBeenCalledWith("7", "always");
  // The request stays, and says which answer is on its way.
  await waitFor(() =>
    expect(card.querySelector(".appr-w")!.textContent).toBe("Always allow"),
  );
  expect(card.querySelector(".appr-w .ring")).toBeTruthy();
  expect(card.querySelector(".appr-a")).toBeNull();
});

it("offers the answers again when one could not be sent", async () => {
  const user = userEvent.setup();
  const { card, onAnswer } = show({ id: "c1", kind: "execute" });
  onAnswer.mockResolvedValueOnce(false);
  await user.click(screen.getByRole("button", { name: "Reject" }));
  await waitFor(() => expect(card.querySelector(".appr-a")).toBeTruthy());
  expect(card.querySelector(".appr-w")).toBeNull();
});

it("stops the turn from its request", async () => {
  const user = userEvent.setup();
  const { onStop } = show({ id: "c1", kind: "execute" });
  await user.click(screen.getByRole("button", { name: "Stop turn" }));
  expect(onStop).toHaveBeenCalledTimes(1);
});

it("tells a caller who may not answer whose answer the turn waits for", () => {
  const { card } = show({ id: "c1", kind: "execute" }, {}, false);
  expect(card.querySelector(".appr-w")!.textContent).toBe(
    "Waiting for Bo Ra to answer",
  );
  expect(card.querySelector(".btn")).toBeNull();
});
