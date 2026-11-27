import { useSyncExternalStore } from "react";
import type { Entry } from "../../../api/types.ts";

// What shows of the files of a session: the directories that are open in
// the tree, the files that are open, and which of them is in view. It lasts
// as long as the page, so that a session shows again as it was left.
export interface Shown {
  // The paths of the open directories. The workspace itself is always open.
  open: ReadonlySet<string>;
  // The paths of the open files, in the order of their tabs.
  tabs: readonly string[];
  // The tab in view.
  at?: string;
  // The files that show as they are written, and not as what they make.
  source: ReadonlySet<string>;
}

const NOTHING: Shown = { open: new Set(), tabs: [], source: new Set() };

const shown = new Map<string, Shown>();
const listeners = new Set<() => void>();

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

function change(session: string, how: (was: Shown) => Shown) {
  shown.set(session, how(shown.get(session) ?? NOTHING));
  for (const listener of listeners) listener();
}

// forget lets go of what showed of the files of every session.
export function forget() {
  shown.clear();
  for (const listener of listeners) listener();
}

// useShown returns what shows of the files of the session.
export function useShown(session: string): Shown {
  return useSyncExternalStore(subscribe, () => shown.get(session) ?? NOTHING);
}

// parentOf returns the path of the directory that holds the path.
export function parentOf(path: string): string {
  const slash = path.lastIndexOf("/");
  return slash < 0 ? "" : path.slice(0, slash);
}

// nameOf returns the last name of the path.
export function nameOf(path: string): string {
  return path.slice(path.lastIndexOf("/") + 1);
}

// join returns the path of a name in a directory.
export function join(directory: string, name: string): string {
  return directory ? directory + "/" + name : name;
}

// above returns the paths of the directories above the path, the nearest to
// the workspace first.
export function above(path: string): string[] {
  const names = path.split("/").slice(0, -1);
  return names.map((_, index) => names.slice(0, index + 1).join("/"));
}

// beside returns the path in the workspace that a file names with an
// address, such as "../charts/a.png": nothing if the address is not one of
// the workspace, leaves it, or names no path.
export function beside(file: string, address: string): string | undefined {
  if (/^([a-z][a-z0-9+.-]*:|\/\/|#)/i.test(address)) return undefined;
  let path = address.split(/[?#]/)[0];
  if (!path) return undefined;
  try {
    path = decodeURIComponent(path);
  } catch {
    // The address is taken as it is written.
  }
  const names = path.startsWith("/")
    ? []
    : parentOf(file).split("/").filter(Boolean);
  for (const name of path.split("/")) {
    if (name === "" || name === ".") continue;
    if (name !== "..") names.push(name);
    else if (!names.length) return undefined;
    else names.pop();
  }
  return names.join("/") || undefined;
}

// toggle opens a directory of the tree, or closes it.
export function toggle(session: string, path: string) {
  change(session, (was) => {
    const open = new Set(was.open);
    if (!open.delete(path)) open.add(path);
    return { ...was, open };
  });
}

// reveal opens the directories above the path, and the path too if it is
// told that it is a directory.
export function reveal(session: string, path: string, itself = false) {
  change(session, (was) => {
    const open = new Set([...was.open, ...above(path)]);
    if (itself && path) open.add(path);
    return { ...was, open };
  });
}

// collapse closes every directory of the tree.
export function collapse(session: string) {
  change(session, (was) => ({ ...was, open: new Set() }));
}

// openFile opens the file in a tab, if it has none, and brings it in view.
export function openFile(session: string, path: string) {
  change(session, (was) => ({
    ...was,
    tabs: was.tabs.includes(path) ? was.tabs : [...was.tabs, path],
    at: path,
  }));
}

// closeFiles closes the tabs of the paths. If the one in view goes, the one
// that came after it comes in view, or else the one before it.
export function closeFiles(session: string, paths: readonly string[]) {
  change(session, (was) => {
    const tabs = was.tabs.filter((path) => !paths.includes(path));
    let at = was.at;
    if (at !== undefined && paths.includes(at)) {
      const from = was.tabs.indexOf(at);
      at =
        was.tabs.slice(from).find((path) => tabs.includes(path)) ?? tabs.at(-1);
    }
    return { ...was, tabs, at };
  });
}

// setSource has a file show as it is written, or as what it makes.
export function setSource(session: string, path: string, on: boolean) {
  change(session, (was) => {
    const source = new Set(was.source);
    if (on) source.add(path);
    else source.delete(path);
    return { ...was, source };
  });
}

// What is known of the files of a session, by path: as the directories that
// were read and the searches that were made told it.
const known = new Map<string, Entry>();
const keyed = (session: string, path: string) => session + "\n" + path;

const watchers = new Set<() => void>();
let telling = false;

// note keeps what a directory, or a search, told of an entry.
export function note(session: string, path: string, entry: Entry) {
  const key = keyed(session, path);
  const had = known.get(key);
  if (
    had &&
    had.size === entry.size &&
    had.modified_at === entry.modified_at &&
    had.type === entry.type
  ) {
    return;
  }
  known.set(key, entry);
  // Those that show the entry are told once of all that changed at a time.
  if (telling) return;
  telling = true;
  queueMicrotask(() => {
    telling = false;
    for (const watcher of watchers) watcher();
  });
}

// useEntry returns what is known of the entry at the path, as it changes.
export function useEntry(session: string, path: string): Entry | undefined {
  return useSyncExternalStore(
    (watcher) => {
      watchers.add(watcher);
      return () => {
        watchers.delete(watcher);
      };
    },
    () => known.get(keyed(session, path)),
  );
}
