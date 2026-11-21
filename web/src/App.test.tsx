// @vitest-environment happy-dom
import { QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { afterEach, beforeAll, expect, it } from "vitest";
import { backend } from "./api/fake.ts";
import { App } from "./App.tsx";
import { prefs } from "./lib/prefs.ts";
import { queryClient } from "./query.ts";
import { signIn, signOut } from "./session/auth.ts";
import { setTheme } from "./theme.ts";

// The tests run the console as development does: the backend of the mock
// answers it, and the accounts of the mock sign in. Alice is an
// administrator, Bob a member of one project, and Dave is in no group that
// may use the installation.
beforeAll(() => {
  prefs.set("mock.speed", "0");
});

afterEach(() => {
  cleanup();
  signOut();
  setTheme("system");
});

// open shows the console at a path, to the account that is signed in.
function open(path: string, account = "") {
  signIn(account);
  render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={[path]}>
        <App />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return userEvent.setup();
}

const toSignIn = { name: /Continue with your organization/ };

it("asks a caller to sign in, and goes on to where they were going", async () => {
  const user = open("/me");
  await user.click(await screen.findByRole("button", toSignIn));
  await user.click(await screen.findByRole("button", { name: /Bob Lee/ }));

  expect(await screen.findByRole("heading", { name: "Bob Lee" })).toBeTruthy();
  expect(screen.getAllByText("bob@example.com").length).toBeGreaterThan(0);
  expect(document.title).toBe("Your account · Aviary");
  expect(prefs.get("mock.token")).toBe("bob");

  await user.click(screen.getByRole("button", { name: "Copy the user ID" }));
  expect((await screen.findByRole("status")).textContent).toContain(
    "the user ID",
  );
});

it("shows the caller's projects", async () => {
  open("/", "bob");
  expect(await screen.findByRole("heading", { name: "Projects" })).toBeTruthy();
  const storefront = screen.getByRole("link", { name: /Storefront/ });
  expect(storefront.getAttribute("href")).toBe("/p/storefront");
  // Bob is no member of the other projects, and no administrator.
  expect(screen.queryByRole("link", { name: /Data Platform/ })).toBeNull();
  expect(screen.getByRole("button", { name: /Bob Lee$/ })).toBeTruthy();
  expect(document.title).toBe("Projects · Aviary");
});

it("signs out from the menu of the account", async () => {
  const user = open("/projects", "alice");
  expect(
    await screen.findByRole("link", { name: /Data Platform/ }),
  ).toBeTruthy();
  await user.click(screen.getByRole("button", { name: /Alice Kim Admin$/ }));
  expect(screen.getByRole("menu").textContent).toContain("alice@example.com");
  await user.click(screen.getByRole("menuitem", { name: "Sign out" }));

  expect(prefs.get("mock.token")).toBeNull();
  expect(queryClient.getQueryData(["projects"])).toBeUndefined();

  // What the console read for Alice is not shown to Bob.
  await user.click(await screen.findByRole("button", toSignIn));
  await user.click(await screen.findByRole("button", { name: /Bob Lee/ }));
  expect(await screen.findByRole("link", { name: /Storefront/ })).toBeTruthy();
  expect(screen.queryByRole("link", { name: /Data Platform/ })).toBeNull();
});

it("changes the theme in the menu, which stays open", async () => {
  const user = open("/projects", "alice");
  await user.click(await screen.findByRole("button", { name: /Alice Kim/ }));
  await user.click(screen.getByRole("button", { name: "Dark" }));
  expect(document.documentElement.dataset.theme).toBe("dark");
  expect(prefs.get("theme")).toBe("dark");
  expect(screen.getByRole("menu")).toBeTruthy();

  await user.keyboard("{Escape}");
  expect(screen.queryByRole("menu")).toBeNull();
});

it("closes the menu on a press outside it, and when its link is followed", async () => {
  const user = open("/projects", "alice");
  const account = await screen.findByRole("button", { name: /Alice Kim/ });
  await user.click(account);
  await user.click(screen.getByRole("heading", { name: "Projects" }));
  expect(screen.queryByRole("menu")).toBeNull();

  await user.click(account);
  await user.click(screen.getByRole("menuitem", { name: "Your account" }));
  expect(await screen.findByText("aviary-admins")).toBeTruthy();
  expect(screen.queryByRole("menu")).toBeNull();
});

it("tells a caller who may not use the installation why", async () => {
  const user = open("/projects", "dave");
  expect(
    await screen.findByRole("heading", { name: "You do not have access" }),
  ).toBeTruthy();
  expect(screen.getByText("dave@example.com")).toBeTruthy();
  expect(screen.getByText("contractors")).toBeTruthy();

  await user.click(screen.getByRole("button", { name: "Sign out" }));
  expect(await screen.findByRole("button", toSignIn)).toBeTruthy();
});

it("drops a token that the API refuses", async () => {
  open("/projects", "ghost");
  expect(await screen.findByRole("button", toSignIn)).toBeTruthy();
  expect(prefs.get("mock.token")).toBeNull();
});

it("has nobody sign in without an identity provider", async () => {
  const mock = await backend();
  mock.setIdentityProvider(false);
  try {
    const user = open("/signin");
    expect(
      await screen.findByRole("heading", { name: "Projects" }),
    ).toBeTruthy();
    await user.click(screen.getByRole("button", { name: /No sign-in/ }));
    const menu = screen.getByRole("menu");
    expect(menu.textContent).toContain("Everyone is an administrator here.");
    expect(screen.queryByRole("menuitem")).toBeNull();
  } finally {
    mock.setIdentityProvider(true);
  }
});

it("says that a path is not one of the console's", async () => {
  const user = open("/nowhere", "alice");
  expect(
    await screen.findByRole("heading", { name: "Not found" }),
  ).toBeTruthy();
  await user.click(screen.getByRole("link", { name: "Go to the projects" }));
  expect(await screen.findByRole("heading", { name: "Projects" })).toBeTruthy();
});
