// @vitest-environment happy-dom
import { focusManager } from "@tanstack/react-query";
import {
  act,
  cleanup,
  fireEvent,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, beforeAll, expect, it, vi } from "vitest";
import { api, ApiError } from "../../api/client.ts";
import { backend } from "../../api/fake.ts";
import { transport } from "../../api/transport.ts";
import { fast, leave, open } from "../../testing.tsx";

beforeAll(fast);
afterEach(async () => {
  vi.restoreAllMocks();
  await leave();
});

const BACKEND = "/p/storefront/agents/backend-engineer";
// The session of the example data whose turn waits for approval.
const RETRY = "Add retry with backoff to the webhook sender";
const REVIEWER = "/p/storefront/agents/reviewer";

// box returns the box in which the message is written.
const box = () =>
  screen.getByRole<HTMLTextAreaElement>("textbox", { name: "Message" });

// bar returns what the composer says below the message.
const bar = () => document.querySelector(".composer .bar")!;

// pathOf returns the path of the session of the backend engineer with the
// title.
async function pathOf(title: string) {
  const mock = await backend();
  const request = (path: string) =>
    mock.request("GET", path, { token: "alice", body: null }).body;
  const { projects } = request("/v1/projects") as {
    projects: { id: string; slug: string }[];
  };
  const storefront = projects.find((each) => each.slug === "storefront")!;
  const { agents } = request(`/v1/projects/${storefront.id}/agents`) as {
    agents: { id: string; slug: string }[];
  };
  const agent = agents.find((each) => each.slug === "backend-engineer")!;
  const { sessions } = request(`/v1/agents/${agent.id}/sessions?limit=100`) as {
    sessions: { id: string; title: string }[];
  };
  return `${BACKEND}/s/${sessions.find((each) => each.title === title)!.id}`;
}

// list returns the list of sessions, once it has some.
async function list() {
  const nav = await screen.findByRole("navigation", { name: "Sessions" });
  await within(nav).findAllByRole("link");
  return within(nav);
}

// talk starts a session of an agent of Storefront, as if elsewhere: of the
// reviewer, who works without asking, unless another is named.
async function talk(title: string, slug = "reviewer") {
  const mock = await backend();
  const request = (method: string, path: string, body: unknown = null) =>
    mock.request(method, path, { token: "alice", body }).body;
  const { projects } = request("GET", "/v1/projects") as {
    projects: { id: string; slug: string }[];
  };
  const storefront = projects.find((each) => each.slug === "storefront")!;
  const { agents } = request("GET", `/v1/projects/${storefront.id}/agents`) as {
    agents: { id: string; slug: string }[];
  };
  const agent = agents.find((each) => each.slug === slug)!;
  const session = request("POST", `/v1/agents/${agent.id}/sessions`, {
    title,
  }) as { id: string };
  // send sends a message, and returns its turn.
  const send = (text: string) =>
    request("POST", `/v1/sessions/${session.id}/messages`, { text }) as {
      id: string;
    };
  return {
    id: session.id,
    agent: agent.id,
    path: `/p/storefront/agents/${slug}/s/${session.id}`,
    request,
    send,
    // say sends a message, and waits for the agent to have answered it.
    async say(text: string) {
      const turn = send(text);
      await waitFor(() => {
        const read = request("GET", "/v1/turns/" + turn.id) as {
          ended_at?: string;
        };
        expect(read.ended_at).toBeTruthy();
      });
    },
  };
}

// deafen has the stream of the project tell nothing from now on, as a
// stream does whose connection hangs.
async function deafen() {
  const carrier = await transport();
  const stream = carrier.stream.bind(carrier);
  vi.spyOn(carrier, "stream").mockImplementation((path, options, handlers) =>
    path.startsWith("/v1/projects/")
      ? stream(path, options, { ...handlers, onEvent: () => {} })
      : stream(path, options, handlers),
  );
}

// refocus has the window lose the focus and get it back, at which the
// console reads again what nothing keeps up to date.
async function refocus() {
  focusManager.setFocused(false);
  focusManager.setFocused(true);
  await new Promise((done) => setTimeout(done, 50));
  focusManager.setFocused(undefined);
}

it("shows an agent with its sessions, and the start of a new one", async () => {
  open(BACKEND, "alice");
  const sessions = (await list()).getAllByRole("link");
  expect(sessions.length).toBe(20);
  expect(sessions[0].textContent).toContain(
    "Explain the bundle rule in the cart",
  );
  expect(sessions[0].textContent).toContain("Completed");
  expect(sessions[1].textContent).toContain("Needs approval");
  expect(sessions[1].querySelector(".ring")).toBeTruthy();
  expect(
    sessions[0].querySelector('[aria-label="Sandbox awake"]'),
  ).toBeTruthy();
  expect(screen.getByRole("heading", { name: "New session" })).toBeTruthy();
  const welcome = document.querySelector(".welcome")!.textContent;
  expect(welcome).toBe(
    "Backend engineerFeatures, fixes and tests for the storefront backend.opencodecoder-largeasks before it acts",
  );
  expect(document.title).toBe("Backend engineer · Storefront · Aviary");
  expect(
    screen.getByRole("link", { name: /New session/ }).getAttribute("href"),
  ).toBe(BACKEND);
  expect(screen.getByRole("link", { name: "Agents" }).className).toBe("on");
});

it("reads older sessions when asked, until there are no more", async () => {
  const user = open(BACKEND, "alice");
  const sessions = await list();
  const before = sessions.getAllByRole("link").length;
  await user.click(screen.getByRole("button", { name: "Load older" }));
  await waitFor(() =>
    expect(sessions.getAllByRole("link").length).toBeGreaterThan(before),
  );
  // The agent has fewer than two pages of sessions.
  expect(sessions.getAllByRole("link").length).toBeLessThan(2 * before);
  expect(screen.queryByRole("button", { name: "Load older" })).toBeNull();
  const ids = sessions
    .getAllByRole("link")
    .map((link) => link.getAttribute("href"));
  expect(new Set(ids).size).toBe(ids.length);

  // The older ones stay when the list is read again.
  await refocus();
  expect(sessions.getAllByRole("link").length).toBe(ids.length);
  expect(screen.queryByRole("button", { name: "Load older" })).toBeNull();
});

it("offers no older sessions of an agent that has none", async () => {
  await talk("One of a few");
  open("/p/storefront/agents/reviewer", "alice");
  const sessions = (await list()).getAllByRole("link");
  expect(sessions.length).toBeLessThan(20);
  expect(screen.queryByRole("button", { name: "Load older" })).toBeNull();
});

it("says whose session each is, and shows only the caller's when asked", async () => {
  const user = open(BACKEND, "alice");
  const sessions = await list();
  const all = sessions.getAllByRole("link");
  expect(all.some((link) => link.textContent?.includes("Bob Lee"))).toBe(true);
  // The caller's own are not marked.
  expect(all[0].textContent).not.toContain("Alice Kim");
  await user.click(screen.getByRole("button", { name: "Mine" }));
  const mine = sessions.getAllByRole("link");
  expect(mine.length).toBeLessThan(all.length);
  expect(mine.some((link) => link.querySelector(".own"))).toBe(false);
  await user.click(screen.getByRole("button", { name: "All" }));
  expect(sessions.getAllByRole("link").length).toBe(all.length);
});

it("shows a member who reads only their own sessions just those", async () => {
  open(BACKEND, "bob");
  const sessions = (await list()).getAllByRole("link");
  expect(sessions.some((link) => link.querySelector(".own"))).toBe(false);
  expect(screen.queryByRole("button", { name: "Mine" })).toBeNull();
  // A user of the project does not manage its agents.
  expect(screen.queryByRole("button", { name: "More" })).toBeNull();
  expect(screen.getByRole("link", { name: /New session/ })).toBeTruthy();
});

it("opens a session at its newest turn", async () => {
  const get = vi.spyOn(api, "get");
  const user = open(BACKEND, "alice");
  const sessions = await list();
  await user.click(
    sessions.getByRole("link", { name: /Why does the checkout test flake/ }),
  );
  expect(
    await screen.findByRole("heading", {
      name: "Why does the checkout test flake on CI?",
    }),
  ).toBeTruthy();
  const head = document.querySelector(".conv-h .tx")!.textContent;
  expect(head).toContain("Completed");
  expect(head).toContain("Asleep");
  expect(head).toMatch(/Started .* · 2 turns$/);
  const turns = await waitFor(() => {
    const found = document.querySelectorAll(".turn");
    expect(found.length).toBe(2);
    return found;
  });
  expect(turns[1].querySelector(".bub-u")!.textContent).toBe("Fix it.");
  expect(turns[1].querySelectorAll(".step").length).toBe(2);
  expect(turns[1].querySelector(".step .answer")!.textContent).toMatch(
    /by Alice Kim$/,
  );
  expect(turns[1].querySelector(".bub-a")!.textContent).toContain(
    "Changed and green",
  );
  // The list marks the session in view.
  const current = sessions.getByRole("link", { current: "page" });
  expect(current.textContent).toContain("Why does the checkout test flake");
  // The turns came with their list: none is read by itself.
  const paths = get.mock.calls.map(([path]) => path);
  expect(paths.filter((path) => path.startsWith("/v1/turns/"))).toEqual([]);
});

it("reads the earlier turns of a long conversation when asked", async () => {
  const session = await talk("A long one");
  for (let seq = 1; seq <= 22; seq++) await session.say("Message " + seq);

  const user = open(session.path, "alice");
  await waitFor(() =>
    expect(document.querySelectorAll(".turn").length).toBe(20),
  );
  const sent = () =>
    [...document.querySelectorAll(".turn .bub-u")].map(
      (bubble) => bubble.textContent,
    );
  expect(sent()[0]).toBe("Message 3");
  expect(sent()[19]).toBe("Message 22");
  expect(document.querySelector(".conv-h")!.textContent).toContain("22 turns");
  await user.click(screen.getByRole("button", { name: /Earlier turns/ }));
  await waitFor(() =>
    expect(document.querySelectorAll(".turn").length).toBe(22),
  );
  expect(sent().slice(0, 3)).toEqual(["Message 1", "Message 2", "Message 3"]);
  expect(screen.queryByRole("button", { name: /Earlier turns/ })).toBeNull();

  // The earlier turns stay when the window gets the focus back.
  await refocus();
  expect(document.querySelectorAll(".turn").length).toBe(22);
});

it("reads the turns of a session again when it is opened again", async () => {
  const session = await talk("Twice");
  await session.say("First");

  const user = open(session.path, "alice");
  await waitFor(() =>
    expect(document.querySelectorAll(".turn").length).toBe(1),
  );
  await user.click(screen.getByRole("link", { name: /New session/ }));
  await screen.findByRole("heading", { name: "New session" });
  await session.say("Second");
  await user.click((await list()).getByRole("link", { name: /Twice/ }));
  await waitFor(() =>
    expect(document.querySelectorAll(".turn").length).toBe(2),
  );
});

it("shows what the agent has said and done so far in a turn that has not ended", async () => {
  const user = open(BACKEND, "alice");
  const sessions = await list();
  const row = () =>
    sessions
      .getAllByRole("link")
      .find((link) => link.textContent?.includes("Add retry with backoff"))!;
  expect(row().textContent).toContain("Needs approval");
  const id = row().getAttribute("href")!.split("/").pop()!;
  await user.click(row());
  const newest = await waitFor(() => {
    const turns = document.querySelectorAll(".turn");
    expect(turns.length).toBe(3);
    return turns[2];
  });
  // The record of a turn that has not ended has none of this: it comes
  // with the events of the turn.
  await waitFor(() =>
    expect(newest.querySelector(".bub-a")?.textContent).toBe(
      "I will create the branch and push it.",
    ),
  );
  expect(newest.querySelector(".chip")!.textContent).toBe("Needs approval");
  expect(newest.textContent).toContain("is waiting for an answer");
  const head = () => document.querySelector(".conv-h .chip")!.textContent;
  expect(head()).toBe("Waiting for approval");
  // The branch was made, with Alice's leave. The push waits for an answer.
  const steps = () => newest.querySelectorAll(".step");
  expect(steps().length).toBe(1);
  expect(steps()[0].textContent).toContain("git checkout -b retry-backoff");
  expect(steps()[0].querySelector(".answer")!.textContent).toMatch(
    /by Alice Kim$/,
  );

  // Someone answers elsewhere, and the turn goes on to its end.
  const mock = await backend();
  const request = (method: string, path: string, body: unknown = null) =>
    mock.request(method, path, { token: "alice", body }).body;
  const waiting = (
    request("GET", "/v1/sessions/" + id) as {
      last_turn: {
        id: string;
        approvals: { id: string; options: { id: string }[] }[];
      };
    }
  ).last_turn;
  const [asked] = waiting.approvals;
  request("POST", `/v1/turns/${waiting.id}/approvals/${asked.id}`, {
    option_id: asked.options[0].id,
  });
  await waitFor(() => expect(head()).toBe("Completed"));
  expect(newest.querySelector(".chip")!.textContent).toBe("Completed");
  expect(steps().length).toBe(2);
  expect(steps()[1].textContent).toContain("git push origin retry-backoff");
  expect(newest.textContent).toContain("Pushed. The branch");
  expect(newest.querySelector(".typing")).toBeNull();
  await waitFor(() => expect(row().textContent).toContain("Completed"));
});

it("shows a turn that begins elsewhere, and follows it", async () => {
  const session = await talk("Joined");
  await session.say("First");
  open(session.path, "alice");
  await waitFor(() =>
    expect(document.querySelectorAll(".turn").length).toBe(1),
  );
  // The agent takes a moment over this one, as agents do.
  const { mock } = await backend();
  mock.speed = 0.05;
  try {
    await session.say("Second?");
  } finally {
    mock.speed = 0;
  }
  const second = await waitFor(() => {
    const turns = document.querySelectorAll(".turn");
    expect(turns.length).toBe(2);
    return turns[1];
  });
  expect(second.querySelector(".bub-u")!.textContent).toBe("Second?");
  await waitFor(() =>
    expect(second.querySelector(".chip")!.textContent).toBe("Completed"),
  );
  expect(second.querySelector(".bub-a")!.textContent).toContain(
    "You said: “Second?”",
  );
  expect(second.querySelector(".done-l")!.textContent).toMatch(/tokens in/);
  const head = document.querySelector(".conv-h")!;
  expect(head.textContent).toContain("2 turns");
  expect(head.querySelector(".chip")!.textContent).toBe("Completed");
});

it("says where a turn that waits is in the queue", async () => {
  const session = await talk("In the queue");
  const user = open(session.path, "alice");
  await screen.findByRole("heading", { name: "In the queue" });
  const { mock } = await backend();
  // The turn waits long enough to be seen waiting.
  mock.speed = 5;
  mock.scenario = "queue";
  try {
    const turn = session.send("Wait for it.");
    const shown = await waitFor(() => {
      const found = document.querySelector(".turn");
      expect(found).toBeTruthy();
      return found!;
    });
    await waitFor(() =>
      expect(shown.querySelector(".chip")!.textContent).toBe("Queued · 3"),
    );
    expect(shown.querySelector(".state span + span")!.textContent).toBe(
      "In the queue, 2 ahead",
    );
    const head = document.querySelector(".conv-h .chip")!;
    expect(head.textContent).toBe("Queued");
    expect(bar().textContent).toBe("QueuedCancel turn");
    expect(turn.id).toBeTruthy();
    await user.click(within(shown as HTMLElement).getByText("Cancel"));
    await waitFor(() =>
      expect(shown.querySelector(".fail")!.textContent).toBe(
        "Cancelled before it started",
      ),
    );
    expect(head.textContent).toBe("Cancelled");
  } finally {
    mock.speed = 0;
    mock.scenario = "auto";
  }
});

it("sends the first message of a new session, which names it", async () => {
  const user = open(REVIEWER, "alice");
  await list();
  expect(document.activeElement).toBe(box());
  // The message waits to be accepted until the test says so.
  const post = api.post;
  let accept: (() => void) | undefined;
  vi.spyOn(api, "post").mockImplementation(async (path, body) => {
    if (path.endsWith("/messages")) {
      await new Promise<void>((resolve) => {
        accept = resolve;
      });
    }
    return post(path, body);
  });
  const message = "What does the cart do with an empty bundle?";
  await user.type(box(), message + "{Enter}");
  // The session is made, named after the message, and opened.
  expect(await screen.findByRole("heading", { name: message })).toBeTruthy();
  // Until the message is accepted it stays in the box, on its way.
  await waitFor(() => expect(accept).toBeTruthy());
  expect(box().value).toBe(message);
  const send = screen.getByRole<HTMLButtonElement>("button", { name: "Send" });
  expect(send.querySelector(".ring")).toBeTruthy();
  expect(document.querySelectorAll(".turn").length).toBe(0);
  accept!();
  const current = (await list()).getByRole("link", { current: "page" });
  expect(current.textContent).toContain(message);
  const turn = await waitFor(() => {
    const found = document.querySelector(".turn");
    expect(found).toBeTruthy();
    return found!;
  });
  expect(turn.querySelector(".bub-u")!.textContent).toBe(message);
  await waitFor(() =>
    expect(turn.querySelector(".chip")!.textContent).toBe("Completed"),
  );
  expect(turn.querySelector(".bub-a")!.textContent).toContain("You said:");
  expect(document.querySelectorAll(".turn").length).toBe(1);
  // The box is ready for the next message.
  expect(box().value).toBe("");
  expect(document.activeElement).toBe(box());
  expect(bar().textContent).toContain("Enter to send");
});

it("sends a message to a session, and shows the turn that it begins", async () => {
  const session = await talk("Typed");
  await session.say("First?");
  const user = open(session.path, "alice");
  await waitFor(() =>
    expect(document.querySelectorAll(".turn").length).toBe(1),
  );
  await user.type(box(), "One{Shift>}{Enter}{/Shift}two?");
  expect(box().value).toBe("One\ntwo?");
  expect(document.querySelectorAll(".turn").length).toBe(1);
  await user.click(screen.getByRole("button", { name: "Send" }));
  const second = await waitFor(() => {
    const turns = document.querySelectorAll(".turn");
    expect(turns.length).toBe(2);
    return turns[1];
  });
  expect(second.querySelector(".bub-u")!.textContent).toBe("One\ntwo?");
  expect(box().value).toBe("");
  await waitFor(() =>
    expect(second.querySelector(".chip")!.textContent).toBe("Completed"),
  );
  expect(document.querySelector(".conv-h")!.textContent).toContain("2 turns");
  // A message that was begun here is not carried to another session.
  await user.type(box(), "Left behind");
  await user.click(screen.getByRole("link", { name: /New session/ }));
  await screen.findByRole("heading", { name: "New session" });
  expect(box().value).toBe("");
});

it("says what the turn is doing where the message is written, and stops it", async () => {
  const session = await talk("To be stopped");
  const user = open(session.path, "alice");
  await screen.findByRole("heading", { name: "To be stopped" });
  const { mock } = await backend();
  // The agent takes long enough over this to be seen at it.
  mock.speed = 5;
  mock.scenario = "long";
  try {
    await user.type(box(), "Read all of it.{Enter}");
    await waitFor(() => expect(bar().textContent).toContain("Starting…"));
    const send = screen.getByRole<HTMLButtonElement>("button", {
      name: "Send",
    });
    expect(send.disabled).toBe(true);
    await user.click(within(bar() as HTMLElement).getByText("Stop turn"));
    await waitFor(() => expect(bar().textContent).toContain("Stopping…"));
    expect(within(bar() as HTMLElement).queryByText("Stop turn")).toBeNull();
    const turn = document.querySelector(".turn")!;
    expect(turn.querySelector(".chip")!.textContent).toBe("Stopping");
  } finally {
    mock.speed = 0;
    mock.scenario = "auto";
  }
});

it("waits before it sends again when the queue is full", async () => {
  const session = await talk("Busy times");
  const user = open(session.path, "alice");
  await screen.findByRole("heading", { name: "Busy times" });
  const refusal = { code: "queue_full", message: "too many turns are queued" };
  const post = vi
    .spyOn(api, "post")
    .mockRejectedValueOnce(new ApiError(429, refusal, { "retry-after": "1" }));
  await user.type(box(), "Now?{Enter}");
  await waitFor(() =>
    expect(bar().textContent).toBe("The queue is full. Try again in 1 s."),
  );
  // The message stays, and is not sent before the wait is over.
  expect(box().value).toBe("Now?");
  await user.type(box(), "{Enter}");
  expect(post).toHaveBeenCalledTimes(1);
  await waitFor(() => expect(bar().textContent).toContain("Enter to send"), {
    timeout: 3000,
  });
  // Without a time from the API the wait is a few seconds.
  post.mockRejectedValueOnce(new ApiError(429, refusal, {}));
  await user.type(box(), "{Enter}");
  await waitFor(() =>
    expect(bar().textContent).toBe("The queue is full. Try again in 5 s."),
  );
  expect(document.querySelectorAll(".turn").length).toBe(0);
});

it("reads a session again that is not as the console has it", async () => {
  // The stream tells the console nothing of what happens elsewhere.
  await deafen();
  const busy = await talk("Sent to twice");
  let user = open(busy.path, "alice");
  await screen.findByRole("heading", { name: "Sent to twice" });
  const { mock } = await backend();
  // The agent takes its time over the message from elsewhere.
  mock.speed = 5;
  try {
    busy.send("From elsewhere?");
    await user.type(box(), "From here{Enter}");
    expect(
      await screen.findByText("The session has an unfinished turn."),
    ).toBeTruthy();
    // The turn from elsewhere shows, and the message stays to be sent.
    await waitFor(() =>
      expect(document.querySelector(".turn .bub-u")?.textContent).toBe(
        "From elsewhere?",
      ),
    );
    expect(box().value).toBe("From here");
    await waitFor(() => expect(bar().textContent).toContain("Starting…"));
  } finally {
    mock.speed = 0;
  }
  await leave();

  const ended = await talk("Ended elsewhere");
  user = open(ended.path, "alice");
  await screen.findByRole("heading", { name: "Ended elsewhere" });
  ended.request("DELETE", "/v1/sessions/" + ended.id);
  await user.type(box(), "Still there?{Enter}");
  expect(await screen.findByText("The session has ended.")).toBeTruthy();
  await waitFor(() =>
    expect(document.querySelector(".gate")?.textContent).toBe(
      "This session has ended.New session",
    ),
  );
});

it("leaves the workspace of an agent that was deleted meanwhile", async () => {
  await deafen();
  const session = await talk("Of an agent that goes");
  const user = open(REVIEWER, "alice");
  await list();
  session.request("DELETE", "/v1/agents/" + session.agent);
  await user.type(box(), "Anyone?{Enter}");
  expect(await screen.findByText("Reviewer was deleted.")).toBeTruthy();
  await waitFor(() =>
    expect(document.title).toBe("Agents · Storefront · Aviary"),
  );
  // The agents were read again, without the one that was deleted.
  await screen.findByText("Backend engineer");
  expect(screen.queryByText("Reviewer")).toBeNull();
});

it("offers to stop a turn only to who may", async () => {
  const session = await talk("Not hers to stop");
  const { mock } = await backend();
  mock.speed = 5;
  mock.scenario = "queue";
  try {
    session.send("Wait for it.");
    // Erin reads every session of Storefront, and controls her own.
    open(session.path, "erin");
    const shown = await waitFor(() => {
      const found = document.querySelector<HTMLElement>(".turn");
      expect(found?.querySelector(".chip")?.textContent).toBe("Queued · 3");
      return found!;
    });
    expect(within(shown).queryByText("Cancel")).toBeNull();
    expect(document.querySelector(".gate")).toBeTruthy();
  } finally {
    mock.speed = 0;
    mock.scenario = "auto";
  }
});

it("says why a message cannot be sent, in the place of its box", async () => {
  const gate = () => document.querySelector(".gate")?.textContent;
  open(await pathOf("Rename the env vars to STOREFRONT_*"), "alice");
  await waitFor(() =>
    expect(gate()).toBe("This session has ended.New session"),
  );
  expect(screen.queryByRole("textbox", { name: "Message" })).toBeNull();
  await leave();

  // Erin reads every session of Storefront, and runs her own.
  open(await pathOf("Summarise the open TODOs in src/cart"), "erin");
  await waitFor(() =>
    expect(gate()).toBe("Only Bob Lee can send messages here."),
  );
  await leave();

  // A turn that failed is not hers to send again.
  open(await pathOf("Bump the payment SDK to v4 and fix what breaks"), "erin");
  await waitFor(() =>
    expect(document.querySelector(".turn .fail")).toBeTruthy(),
  );
  expect(gate()).toBe("Only its creator can send messages here.");
  expect(screen.queryByRole("button", { name: /Send again/ })).toBeNull();
  await leave();

  // She audits Data Platform, and runs no session there.
  open("/p/data-platform/agents/sql-analyst", "erin");
  await waitFor(() =>
    expect(gate()).toBe("Your role cannot run sessions here."),
  );
  expect(screen.queryByRole("link", { name: /New session/ })).toBeNull();
});

it("takes the message of a turn that failed, to send it again", async () => {
  const user = open(
    await pathOf("Bump the payment SDK to v4 and fix what breaks"),
    "alice",
  );
  const again = await screen.findByRole("button", { name: /Send again/ });
  expect(box().value).toBe("");
  await user.click(again);
  const failed = again.closest(".turn")!.querySelector(".bub-u")!.textContent;
  expect(failed).toBeTruthy();
  expect(box().value).toBe(failed);
  expect(document.activeElement).toBe(box());
});

it("says that a turn waits for a sandbox that sleeps to wake", async () => {
  const user = open(
    await pathOf("Why does the checkout test flake on CI?"),
    "alice",
  );
  await waitFor(() =>
    expect(document.querySelector(".conv-h")!.textContent).toContain("Asleep"),
  );
  const { mock } = await backend();
  mock.speed = 5;
  try {
    await user.type(box(), "And now?{Enter}");
    await waitFor(() =>
      expect(
        document.querySelector(".turn:last-of-type .state")?.textContent,
      ).toBe("Waking up…"),
    );
  } finally {
    mock.speed = 0;
  }
});

// card returns the request for approval that waits in the conversation.
const card = () => screen.findByRole("group", { name: "Request for approval" });

// A message that is no question has the agents of the mock work with their
// tools, and the backend engineer asks before it changes a file.
const WORK = "Note down that the total rounds twice, and run the tests.";

it("answers a request for approval, and the turn goes on", async () => {
  const user = open(await pathOf(RETRY), "alice");
  const asking = await card();
  expect(asking.querySelector("h3")!.textContent).toBe(
    "May I run this command?",
  );
  expect(asking.querySelector(".cmd")!.textContent).toBe(
    "$git push origin retry-backoffexecute",
  );
  expect(bar().textContent).toBe("Waiting for approvalStop turn");
  await user.click(within(asking).getByRole("button", { name: "Allow once" }));
  // The harness has its answer, and goes on to the end of the turn.
  await waitFor(() =>
    expect(document.querySelector(".conv-h .chip")!.textContent).toBe(
      "Completed",
    ),
  );
  expect(
    screen.queryByRole("group", { name: "Request for approval" }),
  ).toBeNull();
  const newest = [...document.querySelectorAll(".turn")].at(-1)!;
  const pushed = [...newest.querySelectorAll(".step")].at(-1)!;
  expect(pushed.textContent).toContain("git push origin retry-backoff");
  expect(pushed.querySelector(".answer")!.textContent).toBe(
    "Allow once by Alice Kim",
  );
  expect(newest.textContent).toContain("Pushed. The branch");
});

it("shows the requests that the record of a turn lists before its events come", async () => {
  // The events of the turn never come.
  const carrier = await transport();
  const stream = carrier.stream.bind(carrier);
  vi.spyOn(carrier, "stream").mockImplementation((path, options, handlers) =>
    path.startsWith("/v1/turns/") ? () => {} : stream(path, options, handlers),
  );
  open(await pathOf(RETRY), "alice");
  const asking = await card();
  expect(asking.querySelector(".cmd")!.textContent).toBe(
    "$git push origin retry-backoffexecute",
  );
});

it("refuses what an agent asks to do, one request after another", async () => {
  const session = await talk("Asked twice", "backend-engineer");
  const user = open(session.path, "alice");
  await screen.findByRole("heading", { name: "Asked twice" });
  await user.type(box(), WORK + "{Enter}");
  // The change of a file comes first.
  let asking = await card();
  expect(asking.querySelector("h3")!.textContent).toBe(
    "May I change this file?",
  );
  expect(asking.querySelector(".cmd")!.textContent).toBe(
    "›/home/workspace/NOTES.mdedit",
  );
  expect(asking.querySelector(".code .ad")).toBeTruthy();
  await user.click(within(asking).getByRole("button", { name: "Allow once" }));
  // Then the command, which is refused.
  await waitFor(async () =>
    expect((await card()).querySelector(".cmd")!.textContent).toBe(
      "$npm testexecute",
    ),
  );
  asking = await card();
  await user.click(within(asking).getByRole("button", { name: "Reject" }));
  const turn = document.querySelector(".turn")!;
  await waitFor(() =>
    expect(turn.querySelector(".chip")!.textContent).toBe("Completed"),
  );
  const answers = [...turn.querySelectorAll(".step .answer")];
  expect(answers.map((answer) => answer.textContent)).toEqual([
    "Allow once by Alice Kim",
    "Reject by Alice Kim",
  ]);
  expect(answers[1].className).toContain("no");
});

it("says so when someone else has answered a request first", async () => {
  const user = open(await pathOf(RETRY), "alice");
  const asking = await card();
  const taken = { code: "approval_answered", message: "already answered" };
  const post = vi
    .spyOn(api, "post")
    .mockRejectedValueOnce(new ApiError(409, taken, {}));
  await user.click(within(asking).getByRole("button", { name: "Allow once" }));
  expect(
    await screen.findByText("Someone has already answered this request."),
  ).toBeTruthy();
  // The request waits for the harness to say that it has the answer.
  expect(asking.querySelector(".appr-w")!.textContent).toBe("Allow once");
  expect(post).toHaveBeenCalledTimes(1);
});

it("offers the answers again when one could not be sent", async () => {
  const user = open(await pathOf(RETRY), "alice");
  const asking = await card();
  const refusal = { code: "forbidden", message: "you may not answer this" };
  vi.spyOn(api, "post").mockRejectedValueOnce(new ApiError(403, refusal, {}));
  await user.click(within(asking).getByRole("button", { name: "Reject" }));
  expect(await screen.findByText("You may not answer this.")).toBeTruthy();
  await waitFor(() => expect(asking.querySelector(".appr-a")).toBeTruthy());
});

it("stops a turn from its request for approval", async () => {
  const user = open(await pathOf(RETRY), "alice");
  const asking = await card();
  await user.click(within(asking).getByRole("button", { name: "Stop turn" }));
  const newest = () => [...document.querySelectorAll(".turn")].at(-1)!;
  await waitFor(() =>
    expect(newest().querySelector(".fail")?.textContent).toBe("Stopped"),
  );
  expect(
    screen.queryByRole("group", { name: "Request for approval" }),
  ).toBeNull();
  // The request that was not answered says so.
  expect(newest().querySelector(".answer.no")!.textContent).toBe(
    "Not answered",
  );
});

it("tells a caller who may not answer whose answer a turn waits for", async () => {
  // Erin reads every session of Storefront, and controls her own.
  open(await pathOf(RETRY), "erin");
  const asking = await card();
  expect(asking.querySelector(".appr-w")!.textContent).toMatch(
    /^Waiting for .+ to answer$/,
  );
  expect(within(asking).queryByRole("button")).toBeNull();
});

it("tells the caller of a request that waits in another session", async () => {
  const user = open("/p/storefront", "alice");
  await screen.findByRole("heading", { name: /Storefront/ });
  const session = await talk("Asks elsewhere", "backend-engineer");
  session.send(WORK);
  const told = await screen.findByText("is waiting for you");
  expect(told.textContent).toBe("Backend engineer is waiting for you");
  // A request is told of once, however its session changes meanwhile.
  session.request("PATCH", "/v1/sessions/" + session.id, {
    title: "Asks elsewhere",
  });
  session.request("PATCH", "/v1/sessions/" + session.id, {
    title: "Still asks elsewhere",
  });
  await new Promise((resolve) => setTimeout(resolve, 150));
  expect(screen.getAllByText("is waiting for you").length).toBe(1);
  await user.click(screen.getByRole("link", { name: "Review" }));
  // The request is in the conversation that opens, and is not told of
  // again there.
  expect(
    await screen.findByRole("heading", { name: "Still asks elsewhere" }),
  ).toBeTruthy();
  const asking = await card();
  expect(screen.queryByText("is waiting for you")).toBeNull();
  await user.click(within(asking).getByRole("button", { name: "Allow once" }));
  await waitFor(async () =>
    expect((await card()).querySelector(".cmd")!.textContent).toBe(
      "$npm testexecute",
    ),
  );
  await new Promise((resolve) => setTimeout(resolve, 120));
  expect(screen.queryByText("is waiting for you")).toBeNull();
});

it("tells nobody of a request that is not theirs to answer", async () => {
  open("/p/storefront", "erin");
  await screen.findByRole("heading", { name: /Storefront/ });
  const session = await talk("Not hers to answer", "backend-engineer");
  const turn = session.send(WORK);
  await waitFor(() => {
    const read = session.request("GET", "/v1/turns/" + turn.id) as {
      approvals?: unknown[];
    };
    expect(read.approvals?.length).toBe(1);
  });
  await new Promise((resolve) => setTimeout(resolve, 150));
  expect(screen.queryByText("is waiting for you")).toBeNull();
});

// details returns the pane of the details, and what each of its rows says.
function details() {
  const pane = document.querySelector<HTMLElement>(".pane.details")!;
  const rows = new Map<string, string>();
  for (const name of pane.querySelectorAll("dt")) {
    rows.set(name.textContent, name.nextElementSibling!.textContent);
  }
  return { pane, rows };
}

it("shows the details of a session and of its agent beside the conversation", async () => {
  const path = await pathOf("Why does the checkout test flake on CI?");
  open(path, "alice");
  await waitFor(() =>
    expect(document.querySelectorAll(".turn").length).toBe(2),
  );
  const { pane, rows } = details();
  expect(within(pane).getByRole("heading", { level: 2 }).textContent).toBe(
    "Session details",
  );
  expect(pane.querySelector(".idb span")!.textContent).toBe(
    path.split("/").pop(),
  );
  expect(rows.get("Owner")).toBe("AKAlice Kim");
  expect(rows.get("Started")).toMatch(/^Today \d\d:\d\d$/);
  expect(rows.get("Turns")).toBe("22 completed");
  expect(rows.get("Sandbox")).toBe("Asleep");
  expect(rows.has("Ended")).toBe(false);
  // The tokens are those that its turns took together.
  const mock = await backend();
  const { turns } = mock.request(
    "GET",
    `/v1/sessions/${path.split("/").pop()}/turns`,
    { token: "alice", body: null },
  ).body as { turns: { usage: Record<string, number> }[] };
  const sum = (field: string) =>
    turns
      .reduce((total, turn) => total + turn.usage[field], 0)
      .toLocaleString("en-US");
  expect(pane.querySelector(".tok")!.textContent).toBe(
    `${sum("input_tokens")}in${sum("output_tokens")}out`,
  );
  // And of the agent.
  expect(rows.get("Slug")).toBe("backend-engineer");
  expect(rows.get("Harness")).toBe("opencode");
  expect(rows.get("Model")).toBe("coder-large");
  expect(rows.get("Provider")).toBe("Company gateway");
  expect(rows.get("Approval")).toBe("ask");
  expect(pane.querySelector(".about")!.textContent).toBe(
    "Features, fixes and tests for the storefront backend.",
  );
});

it("leaves the details empty while a session is read, and when there is none", async () => {
  const session = await talk("Read slowly");
  const get = api.get;
  let answer: (() => void) | undefined;
  vi.spyOn(api, "get").mockImplementation(async (asked: string) => {
    if (asked === "/v1/sessions/" + session.id) {
      await new Promise<void>((resolve) => {
        answer = resolve;
      });
    }
    return get(asked);
  });
  open(session.path, "alice");
  await waitFor(() => expect(answer).toBeTruthy());
  expect(details().pane.textContent).toBe("");
  answer!();
  await waitFor(() => expect(details().rows.get("Turns")).toBe("0"));
  await leave();

  open(`${REVIEWER}/s/019b0000-0000-7000-8000-000000000000`, "alice");
  await screen.findByRole("heading", { name: "No such session" });
  expect(details().pane.textContent).toBe("");
});

it("tells of the agent beside the start of a new session", async () => {
  open(REVIEWER, "alice");
  await list();
  const { pane, rows } = details();
  expect(within(pane).getByRole("heading", { level: 2 }).textContent).toBe(
    "About this agent",
  );
  expect(pane.querySelector(".idb")).toBeNull();
  expect([...rows.keys()]).toEqual([
    "Slug",
    "Harness",
    "Model",
    "Provider",
    "Approval",
  ]);
  expect(rows.get("Approval")).toBe("None");
});

it("says of how many turns the details tell when not all were read", async () => {
  const session = await talk("A long one in detail");
  for (let seq = 1; seq <= 22; seq++) await session.say("Message " + seq);
  const user = open(session.path, "alice");
  await waitFor(() =>
    expect(document.querySelectorAll(".turn").length).toBe(20),
  );
  expect(details().rows.get("Turns")).toBe("22of those shown: 20 completed");
  expect(details().pane.querySelector(".r-s h4 small")!.textContent).toBe(
    "of the turns shown",
  );
  await user.click(screen.getByRole("button", { name: /Earlier turns/ }));
  await waitFor(() =>
    expect(details().rows.get("Turns")).toBe("2222 completed"),
  );
  expect(details().pane.querySelector(".r-s h4 small")).toBeNull();
});

it("hides the details, and keeps them so", async () => {
  const path = await pathOf("Why does the checkout test flake on CI?");
  let user = open(path, "alice");
  await waitFor(() =>
    expect(document.querySelectorAll(".turn").length).toBe(2),
  );
  const button = () => screen.getByRole("button", { name: "Details" });
  const frame = () => document.querySelector(".sess")!;
  expect(button().getAttribute("aria-pressed")).toBe("true");
  expect(frame().className).toBe("sess");
  await user.click(button());
  expect(document.querySelector(".pane.details")).toBeNull();
  expect(button().getAttribute("aria-pressed")).toBe("false");
  expect(frame().className).toBe("sess no-details");
  // Also at the start of a new session, and when the console is opened
  // again.
  await user.click(screen.getByRole("link", { name: /New session/ }));
  await screen.findByRole("heading", { name: "New session" });
  expect(document.querySelector(".pane.details")).toBeNull();
  cleanup();
  user = open(path, "alice");
  await waitFor(() =>
    expect(document.querySelectorAll(".turn").length).toBe(2),
  );
  expect(document.querySelector(".pane.details")).toBeNull();
  await user.click(button());
  expect(details().rows.get("Sandbox")).toBe("Asleep");
  // The pane closes itself, too.
  await user.click(
    within(details().pane).getByRole("button", { name: "Close" }),
  );
  expect(document.querySelector(".pane.details")).toBeNull();
  cleanup();
  open(REVIEWER, "alice");
  await list();
  expect(document.querySelector(".pane.details")).toBeNull();
});

// heading returns the title of the session in view, as its head shows it.
const heading = () => document.querySelector(".conv-h h1")!.textContent;

it("renames a session where its title is", async () => {
  const session = await talk("Before");
  const user = open(session.path, "alice");
  await user.click(await screen.findByTitle("Rename the session"));
  const field = () =>
    screen.getByRole<HTMLInputElement>("textbox", {
      name: "Title of the session",
    });
  // The title is there to be written over.
  expect(document.activeElement).toBe(field());
  expect(field().value).toBe("Before");
  expect([field().selectionStart, field().selectionEnd]).toEqual([0, 6]);
  // An input method uses Enter to finish a character.
  await user.keyboard("  After ");
  fireEvent.keyDown(field(), { key: "Enter", isComposing: true });
  expect(field().value).toBe("  After ");
  // Enter keeps the title, without the spaces around it. The field that
  // loses the focus before the API has answered does not keep it again.
  const patched = api.patch;
  let answer: (() => void) | undefined;
  const patch = vi.spyOn(api, "patch").mockImplementation(async (at, body) => {
    await new Promise<void>((resolve) => {
      answer = resolve;
    });
    return patched(at, body);
  });
  await user.keyboard("{Enter}");
  await waitFor(() => expect(answer).toBeTruthy());
  fireEvent.blur(field());
  answer!();
  await waitFor(() => expect(heading()).toBe("After"));
  expect(patch).toHaveBeenCalledTimes(1);
  patch.mockRestore();
  expect(
    screen.queryByRole("textbox", { name: "Title of the session" }),
  ).toBeNull();
  const current = (await list()).getByRole("link", { current: "page" });
  expect(current.textContent).toContain("After");
  const read = () =>
    (session.request("GET", "/v1/sessions/" + session.id) as { title: string })
      .title;
  expect(read()).toBe("After");

  // Escape leaves the title as it was.
  await user.click(screen.getByTitle("Rename the session"));
  await user.keyboard("Not this{Escape}");
  expect(heading()).toBe("After");
  expect(read()).toBe("After");

  // Leaving the field keeps what was written, also nothing.
  await user.click(screen.getByTitle("Rename the session"));
  await user.clear(field());
  await user.click(box());
  await waitFor(() => expect(heading()).toBe("Untitled session"));
  expect(read()).toBe("");
});

it("says so when a session could not be renamed", async () => {
  const session = await talk("Kept");
  const user = open(session.path, "alice");
  await user.click(await screen.findByTitle("Rename the session"));
  const refusal = { code: "forbidden", message: "you may not rename this" };
  const patch = vi
    .spyOn(api, "patch")
    .mockRejectedValueOnce(new ApiError(403, refusal, {}));
  await user.keyboard("Changed{Enter}");
  expect(await screen.findByText("You may not rename this.")).toBeTruthy();
  await waitFor(() => expect(heading()).toBe("Kept"));
  // A title that was not changed is not sent.
  await user.click(screen.getByTitle("Rename the session"));
  await user.keyboard("{Enter}");
  await waitFor(() => expect(heading()).toBe("Kept"));
  expect(patch).toHaveBeenCalledTimes(1);
});

// more opens the menu of the session in view, and returns what it offers.
async function more(user: ReturnType<typeof open>) {
  const head = document.querySelector<HTMLElement>(".conv-h")!;
  await user.click(within(head).getByRole("button", { name: "More" }));
  return screen.getAllByRole("menuitem").map((item) => item.textContent);
}

it("ends a session from its menu, once its turn has ended", async () => {
  const session = await talk("To be ended");
  const user = open(session.path, "alice");
  await screen.findByRole("heading", { name: "To be ended" });
  const { mock } = await backend();
  mock.speed = 5;
  try {
    await user.type(box(), "Take your time.{Enter}");
    await waitFor(() => expect(bar().textContent).toContain("Starting…"));
    expect(await more(user)).toEqual([
      "Rename",
      "Copy the link",
      "Copy the session ID",
      "End the session…",
    ]);
    await user.click(
      screen.getByRole("menuitem", { name: "End the session…" }),
    );
    const dialog = within(
      screen.getByRole("dialog", { name: "End this session?" }),
    );
    await user.click(dialog.getByRole("button", { name: "End session" }));
    expect((await dialog.findByRole("alert")).textContent).toBe(
      "Stop the running turn first.",
    );
    await user.click(dialog.getByRole("button", { name: "Cancel" }));
    mock.speed = 0;
    await user.click(within(bar() as HTMLElement).getByText("Stop turn"));
    await waitFor(() => expect(bar().textContent).toContain("Enter to send"));
  } finally {
    mock.speed = 0;
  }
  await more(user);
  await user.click(screen.getByRole("menuitem", { name: "End the session…" }));
  await user.click(
    within(screen.getByRole("dialog")).getByRole("button", {
      name: "End session",
    }),
  );
  expect(await screen.findByText("The session has ended")).toBeTruthy();
  expect(screen.queryByRole("dialog")).toBeNull();
  // The conversation stays, and takes no more messages.
  expect(document.querySelector(".gate")!.textContent).toBe(
    "This session has ended.New session",
  );
  expect(document.querySelector(".conv-h .chip")!.textContent).toBe("Ended");
  expect(details().rows.has("Ended")).toBe(true);
  expect(details().rows.get("Sandbox")).toBe("Deleted");
  expect(document.querySelectorAll(".turn").length).toBe(1);
  const current = (await list()).getByRole("link", { current: "page" });
  expect(current.textContent).toContain("Ended");
  // A session ends once.
  await more(user);
  const end = screen.getByRole<HTMLButtonElement>("menuitem", {
    name: "End the session…",
  });
  expect(end.disabled).toBe(true);
});

it("copies the link and the ID of a session", async () => {
  const session = await talk("Copied");
  const user = open(session.path, "alice");
  await screen.findByRole("heading", { name: "Copied" });
  const writeText = vi.fn((text: string) => Promise.resolve(text));
  vi.spyOn(navigator.clipboard, "writeText").mockImplementation(
    async (text) => {
      await writeText(text);
    },
  );
  await more(user);
  await user.click(
    screen.getByRole("menuitem", { name: "Copy the session ID" }),
  );
  expect(writeText).toHaveBeenLastCalledWith(session.id);
  expect(await screen.findByText("Copied the session ID")).toBeTruthy();
  await more(user);
  await user.click(screen.getByRole("menuitem", { name: "Copy the link" }));
  expect(writeText.mock.calls.at(-1)![0]).toMatch(
    new RegExp(session.path + "$"),
  );
  expect(await screen.findByText("Copied the link")).toBeTruthy();
});

it("lets only who controls a session rename and end it", async () => {
  // Erin reads every session of Storefront, and controls her own.
  const path = await pathOf("Summarise the open TODOs in src/cart");
  const user = open(path, "erin");
  await screen.findByRole("heading", {
    name: "Summarise the open TODOs in src/cart",
  });
  expect(screen.queryByTitle("Rename the session")).toBeNull();
  expect(await more(user)).toEqual(["Copy the link", "Copy the session ID"]);
});

it("takes the title that the harness suggests, but not over a person's", async () => {
  const user = open(REVIEWER, "alice");
  await list();
  const { mock } = await backend();
  // The session is named after its first message, and then by its harness.
  mock.scenario = "retitle";
  await user.type(box(), "What is in the cart?{Enter}");
  await waitFor(() => expect(heading()).toBe("A title that the harness wrote"));
  const current = (await list()).getByRole("link", { current: "page" });
  expect(current.textContent).toContain("A title that the harness wrote");
  await waitFor(() => expect(bar().textContent).toContain("Enter to send"));

  // A title that a person wrote stays.
  await user.click(screen.getByTitle("Rename the session"));
  await user.keyboard("Mine{Enter}");
  await waitFor(() => expect(heading()).toBe("Mine"));
  mock.scenario = "retitle";
  await user.type(box(), "And now?{Enter}");
  await waitFor(() =>
    expect(document.querySelectorAll(".turn .done-l").length).toBe(2),
  );
  expect(heading()).toBe("Mine");
  await leave();

  // So does one that the session came with.
  const session = await talk("Named elsewhere");
  const again = open(session.path, "alice");
  await screen.findByRole("heading", { name: "Named elsewhere" });
  mock.scenario = "retitle";
  await again.type(box(), "And this one?{Enter}");
  await waitFor(() =>
    expect(document.querySelectorAll(".turn .done-l").length).toBe(1),
  );
  expect(heading()).toBe("Named elsewhere");
});

it("keeps the head of the conversation as its session is", async () => {
  const user = open(BACKEND, "alice");
  const sessions = await list();
  const link = sessions.getByRole("link", { name: /Explain the bundle rule/ });
  const id = link.getAttribute("href")!.split("/").pop()!;
  await user.click(link);
  await screen.findByRole("heading", {
    name: "Explain the bundle rule in the cart",
  });
  const mock = await backend();
  mock.request("PATCH", "/v1/sessions/" + id, {
    token: "alice",
    body: { title: "Renamed elsewhere" },
  });
  expect(
    await screen.findByRole("heading", { name: "Renamed elsewhere" }),
  ).toBeTruthy();
});

it("reads the sessions once the stream of the project is open, and not before", async () => {
  const session = await talk("Read late");
  const carrier = await transport();
  const stream = carrier.stream.bind(carrier);
  // The stream of the project is open when the test says so.
  let opened: (() => void) | undefined;
  vi.spyOn(carrier, "stream").mockImplementation((path, options, handlers) =>
    path.startsWith("/v1/projects/")
      ? stream(path, options, {
          ...handlers,
          onOpen: () => {
            opened = handlers.onOpen;
          },
        })
      : stream(path, options, handlers),
  );
  const get = vi.spyOn(api, "get");
  open(session.path, "alice");
  await waitFor(() => expect(opened).toBeTruthy());
  await new Promise((resolve) => setTimeout(resolve, 60));
  const read = () =>
    get.mock.calls
      .map(([path]) => path)
      .filter(
        (path) =>
          path === "/v1/sessions/" + session.path.split("/").pop() ||
          path.startsWith("/v1/agents/"),
      );
  expect(read()).toEqual([]);
  act(() => opened!());
  expect(
    await screen.findByRole("heading", { name: "Read late" }),
  ).toBeTruthy();
  await list();
  expect(read().length).toBe(2);
});

it("shows a session as the stream told it while it was read", async () => {
  const session = await talk("Before the read");
  const path = "/v1/sessions/" + session.path.split("/").pop();
  const get = api.get;
  vi.spyOn(api, "get").mockImplementation(async (asked: string) => {
    const body = await get(asked);
    if (asked === path) {
      // The stream tells of the change before the answer arrives, which
      // is of the session as it was.
      session.request("PATCH", path, { title: "Renamed during the read" });
      await new Promise((resolve) => setTimeout(resolve, 120));
    }
    return body;
  });
  open(session.path, "alice");
  expect(
    await screen.findByRole("heading", { name: "Renamed during the read" }),
  ).toBeTruthy();
});

it("says why the newest turn of a session failed, in the list and in the conversation", async () => {
  const user = open(BACKEND, "alice");
  const failed = (await list()).getByRole("link", {
    name: /Bump the payment SDK/,
  });
  expect(failed.querySelector(".err")!.textContent).toContain(
    "the model endpoint answered 401",
  );
  await user.click(failed);
  const block = await waitFor(() => {
    const found = document.querySelector(".turn .fail");
    expect(found).toBeTruthy();
    return found!;
  });
  expect(block.textContent).toContain("the model endpoint answered 401");
  expect(document.querySelector(".conv-h .chip")!.textContent).toBe("Failed");
});

it("does not open a session that is not of the agent, or that is not there", async () => {
  open(BACKEND, "alice");
  const first = (await list()).getAllByRole("link")[0];
  const id = first.getAttribute("href")!.split("/").pop()!;
  await leave();

  open(`/p/storefront/agents/reviewer/s/${id}`, "alice");
  expect(
    await screen.findByRole("heading", { name: "No such session" }),
  ).toBeTruthy();
  expect(
    screen
      .getByRole("link", { name: "Start a new session" })
      .getAttribute("href"),
  ).toBe("/p/storefront/agents/reviewer");
  await leave();

  open(`${BACKEND}/s/019b0000-0000-7000-8000-000000000000`, "alice");
  expect(
    await screen.findByRole("heading", { name: "No such session" }),
  ).toBeTruthy();
  await leave();

  // Nor does the console find an agent that the project does not have.
  open("/p/storefront/agents/nobody", "alice");
  expect(
    await screen.findByRole("heading", { name: "Not found" }),
  ).toBeTruthy();
});

it("lists a session that is created elsewhere, and how it changes", async () => {
  const user = open(BACKEND, "alice");
  const sessions = await list();
  const mock = await backend();
  const request = (method: string, path: string, body: unknown = null) =>
    mock.request(method, path, { token: "alice", body }).body;
  const first = sessions.getAllByRole("link")[0];
  const known = request(
    "GET",
    "/v1/sessions/" + first.getAttribute("href")!.split("/").pop(),
  ) as { agent_id: string };
  const made = request("POST", `/v1/agents/${known.agent_id}/sessions`, {
    title: "Made elsewhere",
  }) as { id: string };
  expect(await sessions.findByText("Made elsewhere")).toBeTruthy();
  expect(sessions.getAllByRole("link")[0].textContent).toContain(
    "Made elsewhereToday",
  );
  expect(sessions.getAllByRole("link")[0].textContent).toContain(
    "No messages yet",
  );
  request("PATCH", "/v1/sessions/" + made.id, { title: "Renamed elsewhere" });
  const renamed = await sessions.findByText("Renamed elsewhere");
  expect(sessions.queryByText("Made elsewhere")).toBeNull();

  // A session without a turn opens, and says so.
  await user.click(renamed);
  await screen.findByRole("heading", { name: "Renamed elsewhere" });
  expect(document.querySelector(".conv-h .chip")!.textContent).toBe(
    "No messages yet",
  );
  expect(
    await within(document.querySelector<HTMLElement>(".thread")!).findByText(
      "No messages yet",
    ),
  ).toBeTruthy();
  expect(document.querySelectorAll(".turn").length).toBe(0);
  expect(screen.queryByRole("button", { name: /Earlier turns/ })).toBeNull();
});

it("lets a caller who manages the agents change the agent from its workspace", async () => {
  const user = open(BACKEND, "alice");
  await list();
  await user.click(screen.getByRole("button", { name: "More" }));
  expect(screen.getByRole("menuitem", { name: "Edit agent" })).toBeTruthy();
  expect(screen.getByRole("menuitem", { name: "Delete agent…" })).toBeTruthy();
});
