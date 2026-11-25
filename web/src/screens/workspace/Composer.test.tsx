// @vitest-environment happy-dom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { afterEach, expect, it, vi } from "vitest";
import type { ProjectContext } from "../../api/project.ts";
import type { Agent, Me, Session, Turn } from "../../api/types.ts";
import { barred, type Compose, type Pending } from "./compose.ts";
import { Composer, Gate } from "./Composer.tsx";

afterEach(cleanup);

const agent = { id: "a1", slug: "helper", name: "Helper" } as Agent;

const turn = (more: Partial<Turn>): Turn => ({
  id: "t1",
  session_id: "s1",
  seq: 1,
  state: "running",
  created_at: "2026-11-22T10:00:00Z",
  started_at: "2026-11-22T10:00:02Z",
  ...more,
});

// show draws the composer of a message, and returns what it was given to
// call.
function show(of: Partial<Compose> = {}, live?: Turn, stops = true) {
  const compose: Compose = {
    text: "",
    setText: vi.fn(),
    fill: vi.fn(),
    attach: vi.fn(),
    files: [],
    carries: 0,
    add: vi.fn(),
    remove: vi.fn(),
    sending: false,
    cooldown: 0,
    send: vi.fn(),
    setServers: vi.fn(),
    ...of,
  };
  const onSend = vi.fn();
  const onStop = vi.fn();
  // A project without MCP servers: the composer has no button for them.
  const context = { mcpServers: [] } as unknown as ProjectContext;
  render(
    <QueryClientProvider client={new QueryClient()}>
      <Composer
        context={context}
        agent={agent}
        compose={compose}
        mcp={{ on: [], defaults: [], running: false }}
        turn={live}
        onSend={onSend}
        onStop={stops ? onStop : undefined}
      />
    </QueryClientProvider>,
  );
  const box = screen.getByRole<HTMLTextAreaElement>("textbox", {
    name: "Message",
  });
  const send = screen.getByRole<HTMLButtonElement>("button", { name: "Send" });
  const bar = document.querySelector(".composer .bar")!;
  return { compose, onSend, onStop, box, send, bar };
}

it("takes what is written, and sends it with Enter or the button", async () => {
  const user = userEvent.setup();
  const empty = show();
  expect(empty.box.placeholder).toBe("Message Helper…");
  expect(document.activeElement).toBe(empty.box);
  expect(empty.compose.attach).toHaveBeenCalledWith(empty.box);
  expect(empty.bar.textContent).toBe(
    "Enter to send · Shift + Enter for a new line",
  );
  expect(empty.send.disabled).toBe(true);
  await user.type(empty.box, "H");
  expect(empty.compose.setText).toHaveBeenCalledWith("H");
  // Nothing is sent of an empty box, or of one with only spaces.
  await user.type(empty.box, "{Enter}");
  expect(empty.onSend).not.toHaveBeenCalled();
  cleanup();

  const spaces = show({ text: "  \n " });
  expect(spaces.send.disabled).toBe(true);
  cleanup();

  const written = show({ text: "Hello" });
  expect(written.box.value).toBe("Hello");
  expect(written.send.disabled).toBe(false);
  // Shift and Enter is a new line, and an input method uses Enter to
  // finish a character.
  await user.type(written.box, "{Shift>}{Enter}{/Shift}");
  fireEvent.keyDown(written.box, { key: "Enter", isComposing: true });
  fireEvent.keyDown(written.box, { key: "Enter", keyCode: 229 });
  expect(written.onSend).not.toHaveBeenCalled();
  await user.type(written.box, "{Enter}");
  expect(written.onSend).toHaveBeenCalledTimes(1);
  await user.click(written.send);
  expect(written.onSend).toHaveBeenCalledTimes(2);
});

// file returns a file of a size, and pending the same as a message carries
// it.
const file = (name: string, bytes: number, type = "text/plain") =>
  new File([new Uint8Array(bytes)], name, { type });
const pending = (key: number, of: File, more: Partial<Pending> = {}) => ({
  key,
  file: of,
  ...more,
});

it("offers to attach files only if a message may carry any", async () => {
  const user = userEvent.setup();
  show();
  expect(screen.queryByRole("button", { name: "Attach files" })).toBeNull();
  expect(document.querySelector(".composer input[type=file]")).toBeNull();
  cleanup();

  const { compose, box } = show({ carries: 10 });
  const attach = screen.getByRole("button", { name: "Attach files" });
  const picker = document.querySelector<HTMLInputElement>(
    ".composer input[type=file]",
  )!;
  expect([picker.multiple, picker.hidden]).toEqual([true, true]);
  // The button opens what chooses files.
  const opened = vi.spyOn(picker, "click").mockImplementation(() => {});
  await user.click(attach);
  expect(opened).toHaveBeenCalledTimes(1);
  const chosen = [file("notes.txt", 5), file("image.png", 9, "image/png")];
  fireEvent.change(picker, { target: { files: chosen } });
  expect(compose.add).toHaveBeenCalledWith(chosen);
  // The same file can be chosen again, and the message is written on.
  expect(picker.value).toBe("");
  expect(document.activeElement).toBe(box);
});

it("shows the files that the message carries, and takes one from it", async () => {
  const user = userEvent.setup();
  const files = [
    pending(1, file("checkout-errors.log", 1843200)),
    pending(2, file("결제 웹훅 재시도 설계.pdf", 2411724, "application/pdf")),
  ];
  const { compose, send, onSend, box } = show({ carries: 10, files });
  const list = screen.getByRole("list", { name: "Files" });
  const shown = [...list.querySelectorAll(".file")];
  expect(shown.map((each) => each.querySelector(".what")!.textContent)).toEqual(
    ["checkout-errors.log1.8 MB", "결제 웹훅 재시도 설계.pdf2.3 MB"],
  );
  expect(shown[1].querySelector("b")!.title).toBe("결제 웹훅 재시도 설계.pdf");
  // The files come before the text.
  expect(list.nextElementSibling).toBe(box);
  await user.click(
    screen.getByRole("button", { name: "Remove checkout-errors.log" }),
  );
  expect(compose.remove).toHaveBeenCalledWith(1);
  // A message of files alone can be sent.
  expect(send.disabled).toBe(false);
  await user.type(box, "{Enter}");
  expect(onSend).toHaveBeenCalledTimes(1);
});

it("says how far the files have gone while the message is on its way", () => {
  const files = [
    pending(1, file("gone.txt", 200), { id: "a1" }),
    pending(2, file("going.bin", 200), { sent: 90 }),
    pending(3, file("waiting.txt", 200)),
    pending(4, file("empty.txt", 0), { sent: 0 }),
  ];
  const { box } = show({ carries: 10, files, sending: true, text: "Look" });
  const [gone, going, waiting, empty] = [
    ...document.querySelectorAll(".composer .file"),
  ].map((file) => ({
    says: file.querySelector(".what")!.textContent,
    // The mark of a file that has gone, and the bar of one that goes.
    mark: file.querySelector("small use")?.getAttribute("href"),
    bar: file.querySelector<HTMLElement>(".gone")?.style.width,
  }));
  expect(gone).toEqual({ says: "gone.txt200 bytes", mark: "#i-check" });
  expect(going).toEqual({ says: "going.bin45%", bar: "45%" });
  expect(waiting).toEqual({ says: "waiting.txt200 bytes" });
  expect(empty).toEqual({ says: "empty.txt100%", bar: "100%" });
  // What is on its way is not changed.
  for (const remove of screen.getAllByRole<HTMLButtonElement>("button", {
    name: /^Remove /,
  })) {
    expect(remove.disabled).toBe(true);
  }
  expect(
    screen.getByRole<HTMLButtonElement>("button", { name: "Attach files" })
      .disabled,
  ).toBe(true);
  expect(box.readOnly).toBe(true);
});

it("takes the files that are pasted into the message, and leaves text to the box", () => {
  const { compose, box } = show({ carries: 10 });
  const shot = file("image.png", 9, "image/png");
  const pasted = fireEvent.paste(box, {
    clipboardData: { files: [shot], types: ["Files"] },
  });
  // The paste is the composer's: the box does not get the name of the file.
  expect(pasted).toBe(false);
  expect(compose.add).toHaveBeenCalledWith([shot]);
  const text = fireEvent.paste(box, {
    clipboardData: { files: [], types: ["text/plain"] },
  });
  expect(text).toBe(true);
  expect(compose.add).toHaveBeenCalledTimes(1);
  cleanup();

  // Without files to carry, a paste is the box's.
  const none = show();
  const left = fireEvent.paste(none.box, {
    clipboardData: { files: [shot], types: ["Files"] },
  });
  expect(left).toBe(true);
  expect(none.compose.add).not.toHaveBeenCalled();
});

it("takes the files that are dropped on it, and shows that it would", () => {
  const { compose } = show({ carries: 10 });
  const composer = document.querySelector<HTMLElement>(".composer")!;
  const notes = file("notes.txt", 5);
  const dragged = { dataTransfer: { files: [notes], types: ["Files"] } };
  const box = composer.querySelector("textarea")!;
  fireEvent.dragEnter(composer, dragged);
  expect(composer.classList.contains("over")).toBe(true);
  // Taking the drag is what lets the files be dropped.
  expect(fireEvent.dragOver(composer, dragged)).toBe(false);
  // Going over what is in the composer is not leaving it.
  fireEvent.dragEnter(box, dragged);
  fireEvent.dragLeave(composer, dragged);
  expect(composer.classList.contains("over")).toBe(true);
  fireEvent.dragLeave(box, dragged);
  expect(composer.classList.contains("over")).toBe(false);
  fireEvent.dragEnter(composer, dragged);
  expect(fireEvent.drop(composer, dragged)).toBe(false);
  expect(compose.add).toHaveBeenCalledWith([notes]);
  expect(composer.classList.contains("over")).toBe(false);
  // The next drag begins from outside again.
  fireEvent.dragEnter(composer, dragged);
  fireEvent.dragLeave(composer, dragged);
  expect(composer.classList.contains("over")).toBe(false);
  // What is dragged but is no file is not the composer's.
  const words = { dataTransfer: { files: [], types: ["text/plain"] } };
  fireEvent.dragEnter(composer, words);
  expect(composer.classList.contains("over")).toBe(false);
  expect(fireEvent.dragOver(composer, words)).toBe(true);
  expect(fireEvent.drop(composer, words)).toBe(true);
  expect(compose.add).toHaveBeenCalledTimes(1);
  cleanup();

  // Nor is a file, if messages carry none or one is on its way.
  const none = show();
  const closed = document.querySelector<HTMLElement>(".composer")!;
  fireEvent.dragEnter(closed, dragged);
  expect(closed.classList.contains("over")).toBe(false);
  expect(fireEvent.dragOver(closed, dragged)).toBe(true);
  expect(fireEvent.drop(closed, dragged)).toBe(true);
  expect(none.compose.add).not.toHaveBeenCalled();
  cleanup();
  const busy = show({ carries: 10, sending: true });
  fireEvent.drop(document.querySelector(".composer")!, dragged);
  expect(busy.compose.add).not.toHaveBeenCalled();
});

it("does not send while a message is on its way", async () => {
  const user = userEvent.setup();
  const { box, send, onSend } = show({ text: "Hello", sending: true });
  expect(send.disabled).toBe(true);
  expect(send.querySelector(".ring")).toBeTruthy();
  await user.type(box, "{Enter}");
  expect(onSend).not.toHaveBeenCalled();
});

it("says how long to wait when the queue is full", async () => {
  const user = userEvent.setup();
  const { box, send, bar, onSend } = show(
    { text: "Hello", cooldown: 4 },
    turn({}),
  );
  expect(bar.textContent).toBe("The queue is full. Try again in 4 s.");
  expect(send.disabled).toBe(true);
  expect(screen.queryByRole("button", { name: /turn/ })).toBeNull();
  await user.type(box, "{Enter}");
  expect(onSend).not.toHaveBeenCalled();
});

it("says what the turn that has not ended is doing, and stops it", async () => {
  const user = userEvent.setup();
  const cases: [Partial<Turn>, string, string | null][] = [
    [
      { state: "queued", started_at: undefined, position: 2 },
      "Queued",
      "Cancel turn",
    ],
    [{ state: "queued", started_at: undefined }, "Starting…", "Stop turn"],
    [{ started_at: undefined }, "Starting…", "Stop turn"],
    [{}, "Working…", "Stop turn"],
    [
      { approvals: [{ id: "1", options: [] }] },
      "Waiting for approval",
      "Stop turn",
    ],
    [{ cancel_requested_at: "x" }, "Stopping…", null],
  ];
  for (const [of, doing, button] of cases) {
    const { box, send, bar, onSend, onStop } = show({ text: "Next" }, turn(of));
    expect(bar.querySelector(".msg")!.textContent).toBe(doing);
    // A turn that waits for a person shows the shield, and any other turns.
    const waits = doing === "Waiting for approval";
    expect(!!bar.querySelector(".ring")).toBe(!waits);
    expect(send.disabled).toBe(true);
    await user.type(box, "{Enter}");
    expect(onSend).not.toHaveBeenCalled();
    const stop = screen.queryByRole("button", { name: /turn/ });
    expect(stop?.textContent ?? null).toBe(button);
    if (stop) await user.click(stop);
    expect(onStop).toHaveBeenCalledTimes(button ? 1 : 0);
    cleanup();
  }
  // A caller who may not stop the turn is not offered to.
  show({}, turn({}), false);
  expect(screen.queryByRole("button", { name: /turn/ })).toBeNull();
});

const context = (permissions: string[]) =>
  ({
    project: { slug: "shop" },
    members: [{ user: { id: "u2", username: "bo", display_name: "Bo Ra" } }],
    permissions,
  }) as unknown as ProjectContext;
const me = (more: Partial<Me> = {}) =>
  ({ id: "u1", admin: false, ...more }) as Me;
const session = (more: Partial<Session> = {}) =>
  ({ id: "s1", agent_id: "a1", created_by: "u1", ...more }) as Session;

it("knows who cannot send a message, and why", () => {
  const runs = context(["sessions.run"]);
  const reads = context(["sessions.view_all", "sessions.control_all"]);
  // To a new session.
  expect(barred(runs, me())).toBeUndefined();
  expect(barred(reads, me())).toBe("role");
  // To the caller's own, and to someone else's.
  expect(barred(runs, me(), session())).toBeUndefined();
  expect(barred(reads, me(), session())).toBe("role");
  expect(barred(runs, me(), session({ created_by: "u2" }))).toBe("foreign");
  expect(
    barred(reads, me({ admin: true }), session({ created_by: "u2" })),
  ).toBeUndefined();
  // Without an identity provider every session is the caller's.
  expect(
    barred(runs, me({ id: undefined }), session({ created_by: undefined })),
  ).toBeUndefined();
  expect(barred(runs, me({ admin: true }), session({ ended_at: "x" }))).toBe(
    "ended",
  );
});

it("says why a message cannot be sent, in the place of the composer", () => {
  const gate = (
    why: "role" | "ended" | "foreign",
    of: Partial<Session> = {},
    permissions = ["sessions.run"],
  ) => {
    cleanup();
    render(
      <MemoryRouter>
        <Gate
          why={why}
          context={context(permissions)}
          agent={agent}
          me={me()}
          session={session(of)}
        />
      </MemoryRouter>,
    );
    return document.querySelector(".gate")!;
  };
  expect(gate("role").textContent).toBe("Your role cannot run sessions here.");
  expect(gate("foreign", { created_by: "u2" }).textContent).toBe(
    "Only Bo Ra can send messages here.",
  );
  expect(gate("foreign", { created_by: "u9" }).textContent).toBe(
    "Only its creator can send messages here.",
  );
  const ended = gate("ended", { ended_at: "x" });
  expect(ended.textContent).toBe("This session has ended.New session");
  expect(ended.querySelector("a")!.getAttribute("href")).toBe(
    "/p/shop/agents/helper",
  );
  expect(
    gate("ended", { ended_at: "x", end_reason: "expired" }).textContent,
  ).toBe("This session has expired.New session");
  // A caller who cannot run sessions is not offered a new one, and a
  // deleted agent gets none.
  expect(gate("ended", { ended_at: "x" }, []).textContent).toBe(
    "This session has ended.",
  );
  expect(
    gate("ended", { ended_at: "x", end_reason: "agent_deleted" }).textContent,
  ).toBe("This session ended because its agent was deleted.");
});
