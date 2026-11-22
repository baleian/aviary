import { beforeEach, expect, it, vi } from "vitest";
import { openIssuer } from "./issuer.ts";

// The library that talks to the issuer is stood in for: what it is given,
// and what it answers. A browser and a real issuer check the rest.
const library = vi.hoisted(() => ({
  settings: undefined as unknown,
  getUser: vi.fn(),
  signinRedirect: vi.fn(),
  signinRedirectCallback: vi.fn(),
  signinSilent: vi.fn(),
  removeUser: vi.fn(),
}));

vi.mock("oidc-client-ts", () => ({
  UserManager: class {
    getUser = library.getUser;
    signinRedirect = library.signinRedirect;
    signinRedirectCallback = library.signinRedirectCallback;
    signinSilent = library.signinSilent;
    removeUser = library.removeUser;

    constructor(settings: unknown) {
      library.settings = settings;
    }
  },
}));

const SETTINGS = {
  issuer: "https://login.example.com/realms/main",
  clientId: "aviary",
};
const AFTER = "https://aviary.example.com/signin";

beforeEach(() => {
  vi.resetAllMocks();
});

it("is the client that the settings name, to their issuer", () => {
  openIssuer(SETTINGS, AFTER);
  expect(library.settings).toEqual({
    authority: "https://login.example.com/realms/main",
    client_id: "aviary",
    redirect_uri: "https://aviary.example.com/signin",
    scope: "openid profile email",
    automaticSilentRenew: false,
  });

  openIssuer({ ...SETTINGS, scopes: "openid groups offline_access" }, AFTER);
  expect(library.settings).toMatchObject({
    scope: "openid groups offline_access",
  });
});

it("returns the ID token of the caller who signed in before", async () => {
  const issuer = openIssuer(SETTINGS, AFTER);
  library.getUser.mockResolvedValue({ id_token: "an ID token" });
  expect(await issuer.caller()).toBe("an ID token");
  library.getUser.mockResolvedValue(null);
  expect(await issuer.caller()).toBe("");
});

it("sends the browser to the issuer with where the caller was going", async () => {
  await openIssuer(SETTINGS, AFTER).begin("/p/storefront?tab=agents");
  expect(library.signinRedirect).toHaveBeenCalledWith({
    state: { back: "/p/storefront?tab=agents" },
  });
});

it("ends a sign-in with the ID token and where the caller was going", async () => {
  library.signinRedirectCallback.mockResolvedValue({
    id_token: "an ID token",
    state: { back: "/me" },
  });
  expect(await openIssuer(SETTINGS, AFTER).finish()).toEqual({
    token: "an ID token",
    back: "/me",
  });
});

it("goes on to the start after a sign-in that names no path of the console", async () => {
  const issuer = openIssuer(SETTINGS, AFTER);
  for (const state of [
    undefined,
    {},
    { back: 7 },
    { back: "me" },
    { back: "https://elsewhere.example.com/me" },
    { back: "//elsewhere.example.com/me" },
  ]) {
    library.signinRedirectCallback.mockResolvedValue({ id_token: "t", state });
    expect(await issuer.finish()).toEqual({ token: "t", back: "/" });
  }
});

it("does not end a sign-in that the issuer refused, or that has no ID token", async () => {
  const issuer = openIssuer(SETTINGS, AFTER);
  library.signinRedirectCallback.mockRejectedValue(new Error("access_denied"));
  await expect(issuer.finish()).rejects.toThrow("access_denied");
  library.signinRedirectCallback.mockResolvedValue({ access_token: "only" });
  await expect(issuer.finish()).rejects.toThrow("the issuer sent no ID token");
});

it("renews the ID token with the refresh token", async () => {
  library.getUser.mockResolvedValue({ id_token: "old", refresh_token: "r" });
  library.signinSilent.mockResolvedValue({ id_token: "new" });
  expect(await openIssuer(SETTINGS, AFTER).renew()).toBe("new");
});

it("renews nothing without a refresh token", async () => {
  const issuer = openIssuer(SETTINGS, AFTER);
  library.getUser.mockResolvedValue({ id_token: "old" });
  expect(await issuer.renew()).toBe("");
  library.getUser.mockResolvedValue(null);
  expect(await issuer.renew()).toBe("");
  expect(library.signinSilent).not.toHaveBeenCalled();
});

it("renews nothing if the issuer gives no newer ID token", async () => {
  const issuer = openIssuer(SETTINGS, AFTER);
  library.getUser.mockResolvedValue({ id_token: "old", refresh_token: "r" });
  // The same token again, as from an issuer that renews only access tokens.
  library.signinSilent.mockResolvedValue({ id_token: "old" });
  expect(await issuer.renew()).toBe("");
  library.signinSilent.mockResolvedValue(null);
  expect(await issuer.renew()).toBe("");
  library.signinSilent.mockRejectedValue(new Error("invalid_grant"));
  expect(await issuer.renew()).toBe("");
});

it("drops the tokens of the caller", async () => {
  await openIssuer(SETTINGS, AFTER).forget();
  expect(library.removeUser).toHaveBeenCalledTimes(1);
});
