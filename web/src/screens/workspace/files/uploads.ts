import { useSyncExternalStore } from "react";
import { putFile, refresh } from "../../../api/workspace.ts";
import { join } from "./state.ts";

// A file that is put into a workspace, and where.
export interface Picked {
  // The path of the file below the directory that it is put into: its name,
  // or its place in the folder that it was picked with.
  path: string;
  file: File;
}

// The files of a session that are being put into its workspace: how many,
// how many of them have gone and how many have failed, and the bytes.
export interface Batch {
  total: number;
  done: number;
  failed: number;
  bytes: number;
  sent: number;
  // Whether files are still going.
  going: boolean;
  // Why the first of those that failed did.
  why?: string;
}

interface Running extends Batch {
  // What is left to send, and what gives the uploads up.
  waiting: { to: string; file: File }[];
  stop: AbortController;
  // The bytes of the files that are going, by path.
  partial: Map<string, number>;
  // The files that failed, to send again.
  again: { to: string; file: File }[];
  // When the tree was last told of what has arrived.
  shown: number;
  onDone?: (batch: Batch) => void;
}

// How many files go at a time.
const AT_ONCE = 3;

const batches = new Map<string, Running>();
const listeners = new Set<() => void>();
// What is told of each session, which changes only when its batch does.
const told = new Map<string, Batch | undefined>();

function tell(session: string) {
  const batch = batches.get(session);
  told.set(
    session,
    batch && {
      total: batch.total,
      done: batch.done,
      failed: batch.failed,
      bytes: batch.bytes,
      sent:
        batch.sent +
        [...batch.partial.values()].reduce((sum, part) => sum + part, 0),
      going: batch.going,
      why: batch.why,
    },
  );
  for (const listener of listeners) listener();
}

// useBatch returns the files of the session that are being put into its
// workspace, if there are any.
export function useBatch(session: string): Batch | undefined {
  return useSyncExternalStore(
    (listener) => {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
    () => told.get(session),
  );
}

// upload puts the files into the directory of the session's workspace, a
// few at a time, and adds to what is already going there. onDone is called
// once nothing is left, with how many went and how many failed.
export function upload(
  session: string,
  directory: string,
  picked: Picked[],
  onDone?: (batch: Batch) => void,
) {
  let batch = batches.get(session);
  if (!batch || !batch.going) {
    batch = {
      total: 0,
      done: 0,
      failed: 0,
      bytes: 0,
      sent: 0,
      going: true,
      waiting: [],
      stop: new AbortController(),
      partial: new Map(),
      again: [],
      shown: 0,
    };
    batches.set(session, batch);
  }
  batch.onDone = onDone;
  batch.total += picked.length;
  batch.bytes += picked.reduce((sum, { file }) => sum + file.size, 0);
  batch.waiting.push(
    ...picked.map(({ path, file }) => ({ to: join(directory, path), file })),
  );
  tell(session);
  send(session, batch);
}

// send starts as many of the files that wait as may go at a time.
function send(session: string, batch: Running) {
  if (batch.stop.signal.aborted) return;
  while (batch.partial.size < AT_ONCE && batch.waiting.length) {
    const sending = batch.waiting.shift()!;
    batch.partial.set(sending.to, 0);
    putFile(session, sending.to, sending.file, {
      signal: batch.stop.signal,
      onProgress: (sent) => {
        batch.partial.set(sending.to, sent);
        tell(session);
      },
    })
      .then(
        () => {
          batch.done++;
          batch.sent += sending.file.size;
        },
        (error: unknown) => {
          if (batch.stop.signal.aborted) return;
          batch.failed++;
          batch.again.push(sending);
          batch.why ??= error instanceof Error ? error.message : "";
        },
      )
      .finally(() => {
        batch.partial.delete(sending.to);
        // What the tree shows follows the files as they arrive.
        if (Date.now() - batch.shown > 800) {
          batch.shown = Date.now();
          void refresh(session);
        }
        if (!batch.partial.size && !batch.waiting.length) {
          batch.going = false;
          void refresh(session);
          if (!batch.stop.signal.aborted) batch.onDone?.(batch);
        }
        tell(session);
        send(session, batch);
      });
  }
}

// retry sends again the files of the session that failed.
export function retry(session: string, onDone?: (batch: Batch) => void) {
  const batch = batches.get(session);
  if (!batch || batch.going || !batch.again.length) return;
  const again = batch.again.map(({ to, file }) => ({ path: to, file }));
  batches.delete(session);
  upload(session, "", again, onDone);
}

// dismiss gives up the files of the session that have not gone yet, and
// forgets the batch.
export function dismiss(session: string) {
  batches.get(session)?.stop.abort();
  batches.delete(session);
  tell(session);
  void refresh(session);
}

// fromInput returns the files that were picked with a file input: by name,
// or by their place in the folder if a folder was picked.
export function fromInput(files: FileList): Picked[] {
  return [...files].map((file) => ({
    path: file.webkitRelativePath || file.name,
    file,
  }));
}

// fromDrop returns the files that were dropped, with those of the folders
// among them. What was dropped is read at once: it is gone after the event.
export function fromDrop(data: DataTransfer): Promise<Picked[]> {
  const roots: FileSystemEntry[] = [];
  const plain: File[] = [];
  for (const item of data.items) {
    if (item.kind !== "file") continue;
    const entry = item.webkitGetAsEntry?.();
    if (entry) roots.push(entry);
    else {
      const file = item.getAsFile();
      if (file) plain.push(file);
    }
  }
  return collect(roots).then((found) => [
    ...found,
    ...plain.map((file) => ({ path: file.name, file })),
  ]);
}

async function collect(entries: FileSystemEntry[]): Promise<Picked[]> {
  const picked: Picked[] = [];
  const walk = async (entry: FileSystemEntry, path: string) => {
    if (entry.isFile) {
      const file = await new Promise<File>((resolve, reject) =>
        (entry as FileSystemFileEntry).file(resolve, reject),
      );
      picked.push({ path, file });
      return;
    }
    const reader = (entry as FileSystemDirectoryEntry).createReader();
    // A directory is read a part at a time, until a part is empty.
    for (;;) {
      const part = await new Promise<FileSystemEntry[]>((resolve, reject) =>
        reader.readEntries(resolve, reject),
      );
      if (!part.length) break;
      for (const child of part) await walk(child, path + "/" + child.name);
    }
  };
  for (const entry of entries) await walk(entry, entry.name);
  return picked;
}

// tops returns the names that the files have in the directory that they
// are put into: of the files themselves, and of the folders that hold them.
export function tops(picked: Picked[]): string[] {
  return [...new Set(picked.map(({ path }) => path.split("/")[0]))];
}
