// @vitest-environment happy-dom
import { focusManager } from "@tanstack/react-query";
import { screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeAll, expect, it } from "vitest";
import { backend } from "../../api/fake.ts";
import { fast, leave, open } from "../../testing.tsx";

beforeAll(fast);
afterEach(leave);

const BACKEND = "/p/storefront/agents/backend-engineer";

// list returns the list of sessions, once it has some.
async function list() {
  const nav = await screen.findByRole("navigation", { name: "Sessions" });
  await within(nav).findAllByRole("link");
  return within(nav);
}

// talk starts a session of the reviewer of Storefront, as if elsewhere. The
// agent answers each message that is said at once.
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
  return {
    path: `/p/storefront/agents/reviewer/s/${session.id}`,
    async say(text: string) {
      const turn = request("POST", `/v1/sessions/${session.id}/messages`, {
        text,
      }) as { id: string };
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
