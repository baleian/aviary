// @vitest-environment happy-dom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { afterEach, expect, it, vi } from "vitest";
import type { ProjectContext } from "../../api/project.ts";
import type { Agent, Me, Session, Turn } from "../../api/types.ts";
import { barred, type Compose } from "./compose.ts";
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
    sending: false,
    cooldown: 0,
    send: vi.fn(),
    ...of,
  };
  const onSend = vi.fn();
  const onStop = vi.fn();
  render(
    <Composer
      agent={agent}
      compose={compose}
      turn={live}
      onSend={onSend}
      onStop={stops ? onStop : undefined}
    />,
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
