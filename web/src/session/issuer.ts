import { UserManager } from "oidc-client-ts";
import type { Oidc } from "./settings.ts";

// An Issuer signs callers in with an OpenID Connect issuer. The browser goes
// to the issuer and comes back with a code, which the console exchanges for
// tokens: the authorization code flow with PKCE. The requests carry the ID
// token, which the issuer signs for the console's client as its audience.
// The tokens are kept for the tab, and are gone when it is closed.
export interface Issuer {
  // caller returns the token of the caller who signed in before, or "".
  caller(): Promise<string>;
  // begin sends the browser to the issuer, which sends it back to the
  // console to end up at the path back.
  begin(back: string): Promise<void>;
  // finish ends the sign-in of a browser that the issuer has sent back. It
  // returns the caller's token and the path to end up at, and throws if the
  // issuer did not sign the caller in.
  finish(): Promise<{ token: string; back: string }>;
  // renew returns a token that follows the caller's, or "" if the issuer
  // gives none: the caller has to sign in again.
  renew(): Promise<string>;
  // forget drops the tokens of the caller.
  forget(): Promise<void>;
}

// openIssuer returns the issuer of the settings. The issuer sends the browser
// back to the address after it has signed a caller in: one that is
// registered with it as a redirect URI of the client.
export function openIssuer(settings: Oidc, after: string): Issuer {
  const manager = new UserManager({
    authority: settings.issuer,
    client_id: settings.clientId,
    redirect_uri: after,
    scope: settings.scopes || "openid profile email",
    // A token is renewed when the API refuses it, and not before.
    automaticSilentRenew: false,
  });
  return {
    async caller() {
      return (await manager.getUser())?.id_token ?? "";
    },

    begin(back) {
      return manager.signinRedirect({ state: { back } });
    },

    async finish() {
      const user = await manager.signinRedirectCallback();
      if (!user.id_token) throw new Error("the issuer sent no ID token");
      const { back } = (user.state ?? {}) as { back?: unknown };
      const path = typeof back === "string" && /^\/(?!\/)/.test(back);
      return { token: user.id_token, back: path ? back : "/" };
    },

    async renew() {
      try {
        const user = await manager.getUser();
        // Only a refresh token renews without the caller.
        if (!user?.refresh_token) return "";
        const next = await manager.signinSilent();
        // An issuer that renews no ID token leaves the one it gave before.
        return next?.id_token && next.id_token !== user.id_token
          ? next.id_token
          : "";
      } catch {
        return "";
      }
    },

    forget() {
      return manager.removeUser();
    },
  };
}
