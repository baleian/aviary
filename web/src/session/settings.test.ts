import { afterEach, expect, it, vi } from "vitest";
import { readSettings } from "./settings.ts";

afterEach(() => {
  vi.unstubAllGlobals();
});

// served has the console's own address answer the request for its
// settings.
function served(answer: () => Response | Promise<Response>) {
  const fetched = vi.fn(answer);
  vi.stubGlobal("fetch", fetched);
  return fetched;
}

const json = (body: unknown) =>
  new Response(JSON.stringify(body), {
    headers: { "Content-Type": "application/json" },
  });

it("reads the issuer that signs the callers in", async () => {
  const oidc = {
    issuer: "https://login.example.com/realms/main",
    clientId: "aviary",
    scopes: "openid profile email groups",
  };
  const fetched = served(() => json({ oidc }));
  expect(await readSettings()).toEqual({ oidc });
  expect(fetched).toHaveBeenCalledWith("/config.json", { cache: "no-store" });
});

it("sets nothing for an installation that names no issuer", async () => {
  // What an installation without sign-in is given leaves the names empty.
  for (const body of [
    {},
    null,
    { oidc: { issuer: "", clientId: "", scopes: "" } },
    { oidc: { issuer: "https://login.example.com", clientId: "" } },
  ]) {
    served(() => json(body));
    expect(await readSettings()).toEqual({});
  }
});

it("sets nothing where no settings are served", async () => {
  served(() => new Response("not found", { status: 404 }));
  expect(await readSettings()).toEqual({});
  // A server may answer every path it does not know with the page.
  served(() => new Response("<!doctype html>", { status: 200 }));
  expect(await readSettings()).toEqual({});
  served(() => {
    throw new TypeError("fetch failed");
  });
  expect(await readSettings()).toEqual({});
});
