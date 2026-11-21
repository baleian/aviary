import { useSyncExternalStore } from "react";
import { authorize, onUnauthenticated } from "../api/client.ts";
import { prefs } from "../lib/prefs.ts";
import { queryClient } from "../query.ts";

// Who is signed in: the token that the requests carry. In development the
// accounts of the mock sign in, and the token of one is its name.
const KEY = "mock.token";

let token = prefs.get(KEY) ?? "";
const listeners = new Set<() => void>();

function set(value: string) {
  token = value;
  prefs.set(KEY, value || null);
  authorize(value);
  // What the console has read was read for the caller before.
  queryClient.clear();
  for (const listener of listeners) listener();
}

export function signIn(value: string) {
  set(value);
}

export function signOut() {
  set("");
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

// useToken returns the token of the signed-in caller, or "".
export function useToken(): string {
  return useSyncExternalStore(subscribe, () => token);
}

authorize(token);
// A token that the API refuses has expired, or is not one of this
// installation.
onUnauthenticated(() => {
  if (token) signOut();
});
