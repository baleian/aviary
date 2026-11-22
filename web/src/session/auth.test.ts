// @vitest-environment happy-dom
import { screen } from "@testing-library/react";
import { afterEach, beforeAll, expect, it, vi } from "vitest";
import { api } from "../api/client.ts";
import type { Me } from "../api/types.ts";
import { queryClient } from "../query.ts";
import { fast, leave, open } from "../testing.tsx";
import { begin, signInFailure, signInWith, signOut, signsIn } from "./auth.ts";
import type { Issuer } from "./issuer.ts";

beforeAll(fast);
afterEach(async () => {
  await leave();
  history.replaceState(null, "", "/");
});

// issuer returns an issuer that does only what a test gives it to do. The
// accounts of the mock stand in for its callers: the token of one is its
// name, and the API refuses the token "ghost" as it does one that has
// expired.
function issuer(does: Partial<Issuer> = {}): Issuer {
  return {
    caller: async () => "",
    begin: vi.fn(async () => {}),
    finish: vi.fn(async () => {
      throw new Error("no sign-in to finish");
    }),
    renew: vi.fn(async () => ""),
    forget: vi.fn(async () => {}),
    ...does,
  };
}

const me = () => api.get<Me>("/v1/me");
const toSignIn = { name: /Continue with your organization/ };

it("signs nobody in without an issuer", () => {
  expect(signsIn()).toBe(false);
});

it("makes the caller who signed in before the caller again", async () => {
  const door = issuer({ caller: async () => "alice" });
  await signInWith(door);
  expect(signsIn()).toBe(true);
  expect((await me()).username).toBe("alice@example.com");
  expect(door.finish).not.toHaveBeenCalled();
});

it("ends the sign-in of a browser that the issuer sent back", async () => {
  history.replaceState(null, "", "/signin?code=a-code&state=a-state");
  const finish = vi.fn(async () => ({ token: "bob", back: "/me?from=there" }));
  await signInWith(issuer({ finish }));
  expect(finish).toHaveBeenCalledTimes(1);
  // The browser is where the caller was going, and the code is gone.
  expect(location.pathname + location.search).toBe("/me?from=there");
  expect((await me()).username).toBe("bob@example.com");
  expect(signInFailure()).toBe("");
});

it("says why the issuer did not sign the caller in", async () => {
  history.replaceState(null, "", "/signin?error=access_denied&state=a-state");
  const door = issuer({
    caller: async () => "alice",
    finish: async () => {
      throw new Error("the caller is not allowed to use the client");
    },
  });
  await signInWith(door);
  expect(location.pathname + location.search).toBe("/signin");
  expect(signInFailure()).toBe("the caller is not allowed to use the client");
  await expect(me()).rejects.toMatchObject({ status: 401 });

  const user = open("/signin");
  expect((await screen.findByRole("alert")).textContent).toBe(
    "The caller is not allowed to use the client.",
  );
  // To sign in again is to try again.
  await user.click(screen.getByRole("button", toSignIn));
  expect(door.begin).toHaveBeenCalledWith("/");
  expect(signInFailure()).toBe("");
});

it("takes only what the issuer sends back for the end of a sign-in", async () => {
  // Another path, and the sign-in screen as a caller comes to it.
  for (const path of ["/me?code=a-code&state=a-state", "/signin"]) {
    history.replaceState(null, "", path);
    const door = issuer({ caller: async () => "alice" });
    await signInWith(door);
    expect(door.finish).not.toHaveBeenCalled();
    expect(location.pathname + location.search).toBe(path);
    expect((await me()).username).toBe("alice@example.com");
  }
});

it("renews the token of the caller, who stays signed in", async () => {
  const door = issuer({
    caller: async () => "ghost",
    renew: vi.fn(async () => "alice"),
  });
  await signInWith(door);
  queryClient.setQueryData(["read before"], "kept");
  expect((await me()).username).toBe("alice@example.com");
  expect(door.renew).toHaveBeenCalledTimes(1);
  // What the console has read is still the caller's.
  expect(queryClient.getQueryData(["read before"])).toBe("kept");
  expect(door.forget).not.toHaveBeenCalled();

  // The requests carry the token that followed.
  expect((await me()).username).toBe("alice@example.com");
  expect(door.renew).toHaveBeenCalledTimes(1);
});

it("signs the caller out when the issuer renews nothing", async () => {
  const door = issuer({ caller: async () => "ghost" });
  await signInWith(door);
  queryClient.setQueryData(["read before"], "dropped");
  await expect(me()).rejects.toMatchObject({ status: 401 });
  expect(door.renew).toHaveBeenCalledTimes(1);
  expect(door.forget).toHaveBeenCalledTimes(1);
  expect(queryClient.getQueryData(["read before"])).toBeUndefined();
  // The token had been accepted before: it has only expired.
  expect(signInFailure()).toBe("");
});

it("says that Aviary did not accept a sign-in that it refuses at once", async () => {
  history.replaceState(null, "", "/signin?code=a-code&state=a-state");
  const door = issuer({
    finish: async () => ({ token: "ghost", back: "/projects" }),
  });
  await signInWith(door);
  await expect(me()).rejects.toMatchObject({ status: 401 });
  expect(signInFailure()).toBe("Aviary did not accept the sign-in");
  expect(door.forget).toHaveBeenCalledTimes(1);

  open("/projects");
  expect((await screen.findByRole("alert")).textContent).toBe(
    "Aviary did not accept the sign-in.",
  );
});

it("goes to the issuer to sign in, and comes back to where the caller was going", async () => {
  const door = issuer();
  await signInWith(door);
  const user = open("/p/storefront/agents?kind=all");
  const button = await screen.findByRole("button", toSignIn);
  await user.click(button);
  expect(door.begin).toHaveBeenCalledWith("/p/storefront/agents?kind=all");
  // The browser is leaving: the sign-in does not begin twice.
  expect((button as HTMLButtonElement).disabled).toBe(true);
  expect(screen.queryByRole("dialog")).toBeNull();
});

it("says that the browser could not go to the issuer", async () => {
  const door = issuer({
    begin: vi.fn(async () => {
      throw new Error("failed to fetch");
    }),
  });
  await signInWith(door);
  const user = open("/me");
  const button = await screen.findByRole("button", toSignIn);
  await user.click(button);
  expect((await screen.findByRole("alert")).textContent).toBe(
    "Failed to fetch.",
  );
  expect((button as HTMLButtonElement).disabled).toBe(false);
});

it("forgets the tokens of a caller who signs out", async () => {
  const door = issuer({ caller: async () => "alice" });
  await signInWith(door);
  expect((await me()).username).toBe("alice@example.com");
  signOut();
  expect(door.forget).toHaveBeenCalledTimes(1);
  await expect(me()).rejects.toMatchObject({ status: 401 });
  expect(door.renew).not.toHaveBeenCalled();
});

it("begins a sign-in with the issuer", async () => {
  const door = issuer();
  await signInWith(door);
  await begin("/me");
  expect(door.begin).toHaveBeenCalledWith("/me");
});
