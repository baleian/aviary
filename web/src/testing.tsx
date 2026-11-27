import { QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { expect } from "vitest";
import { backend } from "./api/fake.ts";
import { App } from "./App.tsx";
import { prefs } from "./lib/prefs.ts";
import { queryClient } from "./query.ts";
import { forget } from "./screens/workspace/files/state.ts";
import { signIn, signInWith, signOut } from "./session/auth.ts";
import { setTheme } from "./theme.ts";

// What the tests of the screens share. They run the console as development
// does: the backend of the mock answers it, and the accounts of the mock
// sign in. Alice is an administrator. Carol owns the project Storefront, in
// which Bob is a user, and Bob is an editor of the project internal-tools.
// Dave is in no group that may use the installation.

// fast has the backend of the mock answer at once. A file of tests calls it
// before its tests.
export function fast() {
  prefs.set("mock.speed", "0");
}

// sized gives the window a width in pixels: that of a desktop, until a test
// gives it another.
export function sized(width = 1440) {
  const page = window as unknown as {
    happyDOM: { setViewport(to: { width: number; height: number }): void };
  };
  page.happyDOM.setViewport({ width, height: 900 });
}
sized();

// open shows the console at a path, to the account that is signed in, and
// returns the person who uses it.
export function open(path: string, account = "") {
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

// titled waits for the tab to be named after the screen, which happens a
// moment after the screen is shown.
export function titled(title: string) {
  return waitFor(() => expect(document.title).toBe(title));
}

// leave ends a test: it takes the console away, and puts the browser and
// the example data back as they began.
export async function leave() {
  cleanup();
  signOut();
  await signInWith(undefined);
  setTheme("system");
  sized();
  for (const kept of [
    "project",
    "list",
    "pane",
    "pane.details",
    "pane.tree",
    "pane.files",
    "files.tree",
    "files.width",
  ]) {
    prefs.set(kept, null);
  }
  forget();
  (await backend()).reset();
}
