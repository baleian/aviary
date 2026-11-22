import { useSyncExternalStore } from "react";
import { authorize, onUnauthenticated, renewWith } from "../api/client.ts";
import { prefs } from "../lib/prefs.ts";
import { queryClient } from "../query.ts";
import type { Issuer } from "./issuer.ts";
import { readSettings } from "./settings.ts";

// Who is signed in: the token that the requests carry. The OpenID Connect
// issuer of the installation signs callers in, if the installation names
// one. In development the accounts of the mock sign in instead, and the
// token of one is its name.
const KEY = "mock.token";

// The path that the issuer sends the browser back to: the sign-in screen.
const RETURN = "/signin";

// Whether somebody is signed in.
let signedIn = false;
// Changes when the caller does, and not when the caller's token is renewed.
let caller = 0;
let issuer: Issuer | undefined;
// Why the last sign-in failed, or "".
let failure = "";
// When the issuer signed the caller in, in ms.
let entered = 0;
const listeners = new Set<() => void>();

// How long after a sign-in a refused token counts as never accepted, in ms.
const FRESH = 10_000;

// enter makes the bearer of a token the caller: "" for nobody.
function enter(token: string) {
  signedIn = token !== "";
  authorize(token);
  caller++;
  // What the console has read was read for the caller before.
  queryClient.clear();
  for (const listener of listeners) listener();
}

// start finds out who is signed in. The console shows after it, so that its
// first requests carry the caller's token.
export async function start(): Promise<void> {
  if (__FAKE_API__) return;
  const { oidc } = await readSettings();
  if (!oidc) return;
  // What talks to the issuer is loaded only where there is one.
  const { openIssuer } = await import("./issuer.ts");
  await signInWith(openIssuer(oidc, location.origin + RETURN));
}

// signInWith has an issuer sign the callers in: undefined for none. The
// caller who signed in before is the caller again. If the issuer has just
// sent the browser back, the sign-in ends here, and the browser goes on to
// where the caller was going.
export async function signInWith(door: Issuer | undefined): Promise<void> {
  issuer = door;
  renewWith(door && renew);
  failure = "";
  entered = 0;
  if (!door) return;
  const query = new URLSearchParams(location.search);
  if (location.pathname !== RETURN || !query.has("state")) {
    enter(await door.caller());
    return;
  }
  try {
    const { token, back } = await door.finish();
    history.replaceState(null, "", back);
    entered = Date.now();
    enter(token);
  } catch (error) {
    history.replaceState(null, "", RETURN);
    failure = error instanceof Error ? error.message : String(error);
  }
}

// renew has the issuer renew the token of the caller, which the API has
// refused.
async function renew() {
  const next = await issuer?.renew();
  if (next) authorize(next);
}

// signsIn reports whether the installation names an issuer for the console
// to sign callers in with.
export function signsIn(): boolean {
  return !!issuer;
}

// begin sends the browser to the issuer to sign a caller in, who then comes
// back to the path of the console.
export function begin(back: string): Promise<void> {
  failure = "";
  return issuer!.begin(back);
}

// signInFailure returns why the last sign-in failed, or "".
export function signInFailure(): string {
  return failure;
}

// signIn signs in an account of the mock, by its token.
export function signIn(token: string) {
  prefs.set(KEY, token || null);
  enter(token);
}

export function signOut() {
  prefs.set(KEY, null);
  void issuer?.forget();
  enter("");
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

// useCaller returns a number that changes whenever the caller does.
export function useCaller(): number {
  return useSyncExternalStore(subscribe, () => caller);
}

if (__FAKE_API__) enter(prefs.get(KEY) ?? "");
// A token that the API refuses has expired and could not be renewed, or is
// not one of this installation.
onUnauthenticated(() => {
  if (!signedIn) return;
  if (Date.now() - entered < FRESH) {
    failure = "Aviary did not accept the sign-in";
  }
  signOut();
});
