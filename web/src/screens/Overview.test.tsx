// @vitest-environment happy-dom
import { screen, within } from "@testing-library/react";
import { afterEach, beforeAll, expect, it } from "vitest";
import { backend } from "../api/fake.ts";
import { fast, leave, open } from "../testing.tsx";

beforeAll(fast);
afterEach(leave);

// part returns the part of the page with the class, once the sessions have
// been read.
async function part(name: string) {
  await screen.findAllByRole("link", { name: /Backend engineer/ });
  return within(document.querySelector<HTMLElement>("." + name)!);
}

it("greets the caller and sums up the project", async () => {
  open("/p/storefront", "alice");
  const hero = await part("hero");
  expect(
    hero.getByText(/^Good (morning|afternoon|evening), Alice$/),
  ).toBeTruthy();
  expect(hero.getByRole("heading", { name: /Storefront/ })).toBeTruthy();
  const facts = document.querySelector(".hero .facts")!.textContent;
  expect(facts).toBe("4agents2model providers1secret store5members");
  expect(document.title).toBe("Overview · Storefront · Aviary");
  expect(screen.getByRole("link", { name: "Overview" }).className).toBe("on");
});

it("points at the session that waits for the caller", async () => {
  open("/p/storefront", "alice");
  const nudge = await screen.findByRole("link", {
    name: /Backend engineer is waiting for you/,
  });
  expect(nudge.textContent).toContain(
    "Wants to run git push origin retry-backoff",
  );
  expect(nudge.getAttribute("href")).toMatch(
    /^\/p\/storefront\/agents\/backend-engineer\/s\/[0-9a-f-]{36}$/,
  );
  // The card of its agent says so too, and those of the others do not.
  const cards = [...document.querySelectorAll(".agents .agent")];
  expect(cards.map((card) => card.querySelector(".chip")?.textContent)).toEqual(
    ["Needs you", undefined, undefined, undefined],
  );
});

it("lists the caller's newest sessions of every agent", async () => {
  open("/p/storefront", "alice");
  const rows = await (await part("rows")).findAllByRole("link");
  expect(rows.length).toBe(6);
  expect(rows[0].textContent).toContain("Explain the bundle rule in the cart");
  expect(rows[0].textContent).toContain("Backend engineer");
  expect(rows[0].textContent).toContain("Completed");
  expect(rows[1].textContent).toContain("Waiting for approval");
  expect(rows[1].querySelector(".ring")).toBeTruthy();
  // A turn that failed says why.
  const failed = rows.find((row) => row.textContent?.includes("Failed"))!;
  expect(failed.textContent).toContain("the model endpoint answered 401");
  // Sessions of two agents are in one list, newest first.
  expect(rows[3].textContent).toContain("Reviewer");
  expect(rows[0].getAttribute("href")).toMatch(
    /^\/p\/storefront\/agents\/backend-engineer\/s\//,
  );
});

it("shows no more than the newest few sessions", async () => {
  const mock = await backend();
  const projects = mock.request("GET", "/v1/projects", {
    token: "alice",
    body: null,
  }).body as { projects: { id: string; slug: string }[] };
  const storefront = projects.projects.find(
    (project) => project.slug === "storefront",
  )!;
  const agents = mock.request("GET", `/v1/projects/${storefront.id}/agents`, {
    token: "alice",
    body: null,
  }).body as { agents: { id: string; slug: string }[] };
  const writer = agents.agents.find((agent) => agent.slug === "docs-writer")!;
  for (const title of ["First of two", "Second of two"]) {
    mock.request("POST", `/v1/agents/${writer.id}/sessions`, {
      token: "alice",
      body: { title },
    });
  }

  open("/p/storefront", "alice");
  const rows = await (await part("rows")).findAllByRole("link");
  expect(rows.length).toBe(6);
  expect(rows[0].textContent).toContain("Second of two");
  expect(rows[0].textContent).toContain("No messages yet");
  expect(rows[1].textContent).toContain("First of two");
});

it("shows only the caller's own sessions, and what their role allows", async () => {
  open("/p/storefront", "bob");
  await screen.findAllByRole("link", { name: /Backend engineer/ });
  const rows = within(document.querySelector<HTMLElement>(".rows")!);
  for (const row of await rows.findAllByRole("link")) {
    expect(row.textContent).not.toContain("Explain the bundle rule");
  }
  expect(screen.queryByRole("button", { name: /New agent/ })).toBeNull();
  expect(screen.getByRole("link", { name: "See all" })).toBeTruthy();
  expect(screen.getByRole("link", { name: "See access" })).toBeTruthy();
});

it("sums up the connections and the members", async () => {
  open("/p/storefront", "alice");
  const connections = (await part("conn")).getAllByRole("listitem");
  expect(connections.map((item) => item.textContent)).toEqual([
    "Company gatewayhttps://llm.example.com/v1Key stored",
    "Local model serverhttp://10.0.0.5:8000/v1Key in Platform secrets",
    "Platform secretshttps://secrets.example.com:8200 · mount secretkv-v2",
  ]);
  expect(
    screen.getByRole("link", { name: "Manage" }).getAttribute("href"),
  ).toBe("/p/storefront/connections");

  const members = document.querySelector(".mstrip")!;
  expect(members.querySelector("p")!.textContent).toBe(
    "5 members · 3 people, 1 group, 1 awaited",
  );
  expect(members.querySelector(".by")!.textContent).toBe(
    "ownereditoruserviewercustom",
  );
  expect(members.querySelectorAll(".stack .av").length).toBe(5);
  expect(
    screen.getByRole("link", { name: "Manage access" }).getAttribute("href"),
  ).toBe("/p/storefront/access");
});

it("says of a provider without a key that it has none", async () => {
  open("/p/data-platform", "alice");
  const provider = await screen.findByText("In-cluster model server");
  expect(provider.closest("li")!.textContent).toContain("No key");
  expect(screen.getByText("None yet")).toBeTruthy();
});

it("leads a project without agents through its first steps", async () => {
  open("/p/playground", "alice");
  const provider = await screen.findByRole("link", { name: "Add a provider" });
  expect(provider.getAttribute("href")).toBe("/p/playground/connections");
  // The second step waits for the first.
  expect(screen.queryByRole("button", { name: "New agent" })).toBeNull();
  expect(screen.getByText("No sessions yet")).toBeTruthy();
  expect(screen.getByText("No members yet")).toBeTruthy();
  expect(screen.getAllByText("None yet").length).toBe(2);

  // With a provider the second step is next, and opens the dialog.
  await leave();
  const mock = await backend();
  const projects = mock.request("GET", "/v1/projects", {
    token: "alice",
    body: null,
  }).body as { projects: { id: string; slug: string }[] };
  const playground = projects.projects.find(
    (project) => project.slug === "playground",
  )!;
  mock.request("POST", `/v1/projects/${playground.id}/providers`, {
    token: "alice",
    body: { name: "Gateway", url: "https://llm.example.com/v1" },
  });
  const user = open("/p/playground", "alice");
  await user.click(await screen.findByRole("button", { name: "New agent" }));
  expect(screen.getByRole("dialog", { name: "New agent" })).toBeTruthy();
  expect(screen.queryByRole("link", { name: "Add a provider" })).toBeNull();
});

it("says that everyone is an administrator without an identity provider", async () => {
  (await backend()).setIdentityProvider(false);
  open("/p/playground");
  expect(
    await screen.findByText("No sign-in: everyone is an administrator"),
  ).toBeTruthy();
  expect(screen.getByText(/^Good (morning|afternoon|evening)$/)).toBeTruthy();
});
