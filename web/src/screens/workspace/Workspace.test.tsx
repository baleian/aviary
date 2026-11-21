// @vitest-environment happy-dom
import { focusManager } from "@tanstack/react-query";
import { act, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeAll, expect, it, vi } from "vitest";
import { api } from "../../api/client.ts";
import { backend } from "../../api/fake.ts";
import { transport } from "../../api/transport.ts";
import { fast, leave, open } from "../../testing.tsx";

beforeAll(fast);
afterEach(async () => {
  vi.restoreAllMocks();
  await leave();
});

const BACKEND = "/p/storefront/agents/backend-engineer";

// list returns the list of sessions, once it has some.
async function list() {
  const nav = await screen.findByRole("navigation", { name: "Sessions" });
  await within(nav).findAllByRole("link");
  return within(nav);
}

// talk starts a session of the reviewer of Storefront, as if elsewhere.
async function talk(title: string) {
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
  const reviewer = agents.find((each) => each.slug === "reviewer")!;
  const session = request("POST", `/v1/agents/${reviewer.id}/sessions`, {
    title,
  }) as { id: string };
  // send sends a message, and returns its turn.
  const send = (text: string) =>
    request("POST", `/v1/sessions/${session.id}/messages`, { text }) as {
      id: string;
    };
  return {
    path: `/p/storefront/agents/reviewer/s/${session.id}`,
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
  const head = document.querySelector(".conv-h")!.textContent;
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
  open(session.path, "alice");
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
    expect(shown.querySelector(".state")!.textContent).toBe(
      "In the queue, 2 ahead",
    );
    const head = document.querySelector(".conv-h .chip")!;
    expect(head.textContent).toBe("Queued");
    session.request("POST", `/v1/turns/${turn.id}/cancel`);
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
