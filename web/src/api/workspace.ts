import { useQueries, useQuery } from "@tanstack/react-query";
import { queryClient } from "../query.ts";
import { api } from "./client.ts";
import type { Entry, Found, Listing } from "./types.ts";

// The files of a session's workspace: what its sandbox has under
// /home/workspace. A path is relative to the workspace, and "" is the
// workspace itself.

const base = (session: string) => `/v1/sessions/${session}/files`;
const at = (path: string) => "path=" + encodeURIComponent(path);

const listingKey = (session: string, path: string) => [
  "files",
  session,
  "list",
  path,
];

// How many pages of a directory have been read, by session and path: a
// directory that is read again is read as far as it was.
const pages = new Map<string, number>();
const paged = (session: string, path: string) => session + "\n" + path;

async function page(
  session: string,
  path: string,
  after?: string,
  limit?: number,
): Promise<Listing> {
  const from = after ? "&after=" + encodeURIComponent(after) : "";
  const most = limit ? "&limit=" + limit : "";
  return api.get<Listing>(`${base(session)}?${at(path)}${from}${most}`);
}

async function listing(session: string, path: string): Promise<Listing> {
  const wanted = pages.get(paged(session, path)) ?? 1;
  let read = await page(session, path);
  const entries = read.entries;
  for (let more = 1; more < wanted && read.next; more++) {
    read = await page(session, path, read.next);
    entries.push(...read.entries);
  }
  return { entries, next: read.next };
}

// useListings reads the directories of a workspace at the paths, each as
// far as more() has taken it. They are read again only when refresh() says
// so: reading wakes a sandbox that sleeps.
export function useListings(session: string, paths: string[], reads: boolean) {
  return useQueries({
    queries: paths.map((path) => ({
      queryKey: listingKey(session, path),
      queryFn: () => listing(session, path),
      enabled: reads,
      staleTime: Infinity,
      refetchOnWindowFocus: false,
      retry: false,
    })),
  });
}

// more reads the next page of a directory, after those that were read.
export async function more(session: string, path: string): Promise<void> {
  const key = listingKey(session, path);
  const had = queryClient.getQueryData<Listing>(key);
  if (!had?.next) return;
  const read = await page(session, path, had.next);
  pages.set(paged(session, path), (pages.get(paged(session, path)) ?? 1) + 1);
  queryClient.setQueryData<Listing>(key, (now) =>
    now && now.next === had.next
      ? { entries: [...now.entries, ...read.entries], next: read.next }
      : now,
  );
}

// refresh reads again what was read of a workspace: its directories, and
// the files that are shown.
export function refresh(session: string): Promise<void> {
  return queryClient.invalidateQueries({ queryKey: ["files", session] });
}

// readNames reads the names of all the entries of a directory.
export async function readNames(
  session: string,
  path: string,
): Promise<Set<string>> {
  const names = new Set<string>();
  let read: Listing | undefined;
  do {
    read = await page(session, path, read?.next, 1000);
    for (const entry of read.entries) names.add(entry.name);
  } while (read.next);
  return names;
}

// readFile reads a file of a workspace, or no more than its first bytes if
// it is told how many.
export function readFile(
  session: string,
  path: string,
  first?: number,
): Promise<Blob> {
  return api.download(`${base(session)}/content?${at(path)}`, first);
}

// readArchive reads a directory of a workspace as a ZIP file.
export function readArchive(session: string, path: string): Promise<Blob> {
  return api.download(`${base(session)}/archive?${at(path)}`);
}

// putFile writes a file into a workspace, at the path, in place of the one
// that is there. The directories above it are made.
export function putFile(
  session: string,
  path: string,
  file: Blob,
  options?: { onProgress?: (sent: number) => void; signal?: AbortSignal },
): Promise<Entry> {
  return api.store<Entry>(
    `${base(session)}/content?${at(path)}`,
    file,
    options,
  );
}

// useSearch finds the files and directories of a workspace whose names have
// the words.
export function useSearch(session: string, words: string) {
  return useQuery({
    queryKey: ["files", session, "search", words],
    queryFn: () =>
      api.get<{ entries: Found[]; truncated: boolean }>(
        `${base(session)}/search?query=${encodeURIComponent(words)}`,
      ),
    enabled: words !== "",
    staleTime: Infinity,
    refetchOnWindowFocus: false,
    retry: false,
  });
}
