import { type ReactNode, useSyncExternalStore } from "react";
import { sentence } from "../lib/format.ts";

export interface Toast {
  id: number;
  message: ReactNode;
  kind: "" | "ok" | "bad";
  // A link that goes with the message.
  action?: ReactNode;
}

let shown: Toast[] = [];
let last = 0;
const listeners = new Set<() => void>();

function show(toasts: Toast[]) {
  shown = toasts;
  for (const listener of listeners) listener();
}

export function dismiss(id: number) {
  show(shown.filter((toast) => toast.id !== id));
}

// toast shows a message for a while. Two show at most: a newer one is worth
// more than an older one.
export function toast(
  message: ReactNode,
  kind: Toast["kind"] = "",
  ms = 3600,
  action?: ReactNode,
) {
  const id = ++last;
  show([...shown.slice(-1), { id, message, kind, action }]);
  setTimeout(() => dismiss(id), ms);
}

// oops shows a failed request as a toast.
export function oops(error: unknown) {
  const message = error instanceof Error ? error.message : "";
  toast(sentence(message), "bad", 5200);
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

export function useToasts(): Toast[] {
  return useSyncExternalStore(subscribe, () => shown);
}
