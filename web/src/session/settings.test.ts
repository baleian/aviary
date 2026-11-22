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

it("reads the address of an API that is not at the console's own", async () => {
  for (const [api, address] of [
    ["https://api.example.com", "https://api.example.com"],
    // The paths of the API begin with a slash.
    ["https://api.example.com/", "https://api.example.com"],
    ["https://example.com/aviary//", "https://example.com/aviary"],
  ]) {
    served(() => json({ api }));
    expect(await readSettings()).toEqual({ api: address });
  }
  const oidc = { issuer: "https://login.example.com", clientId: "aviary" };
  served(() => json({ api: "https://api.example.com", oidc }));
  expect(await readSettings()).toEqual({
    api: "https://api.example.com",
    oidc,
  });
});

it("sets nothing for an installation that names no issuer", async () => {
  // What an installation without sign-in is given leaves the names empty.
  for (const body of [
    {},
    null,
    { api: "", oidc: { issuer: "", clientId: "", scopes: "" } },
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
