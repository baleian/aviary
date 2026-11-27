import { useVirtualizer } from "@tanstack/react-virtual";
import {
  type DragEvent,
  type KeyboardEvent,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { ApiError } from "../../../api/client.ts";
import { saveFile, useLimits } from "../../../api/conversation.ts";
import type { Entry, Listing, Session } from "../../../api/types.ts";
import {
  more,
  readArchive,
  readFile,
  readNames,
  refresh,
  useListings,
  useSearch,
} from "../../../api/workspace.ts";
import { cls } from "../../../lib/cls.ts";
import { plural, sentence, size } from "../../../lib/format.ts";
import { Empty, Loading } from "../../../ui/Blank.tsx";
import { copy } from "../../../ui/clipboard.ts";
import { Dialog } from "../../../ui/Dialog.tsx";
import { Icon } from "../../../ui/Icon.tsx";
import { Menu, MenuItem } from "../../../ui/Menu.tsx";
import { oops, toast } from "../../../ui/toast.ts";
import { sortOf } from "./kinds.ts";
import {
  above,
  collapse,
  join,
  nameOf,
  note,
  openFile,
  parentOf,
  reveal,
  type Shown,
  toggle,
} from "./state.ts";
import {
  type Batch,
  dismiss,
  fromDrop,
  fromInput,
  type Picked,
  retry,
  tops,
  upload,
  useBatch,
} from "./uploads.ts";

// Where the workspace is in the sandbox, as the agent names it.
const ROOT = "/home/workspace";

// How tall a row of the tree is, in pixels.
const ROW = 28;

// A row of the tree: an entry, or what stands for the entries of a
// directory that are not shown.
interface Row {
  // "entry" is a file or a directory. "more" stands for the entries of a
  // directory that were not read, "wait" for those that are being read,
  // "fail" for those that could not be, and "none" for those of an empty
  // directory.
  is: "entry" | "more" | "wait" | "fail" | "none";
  // The path of the entry, or of the directory that the row is about.
  path: string;
  depth: number;
  entry?: Entry;
  open?: boolean;
  why?: string;
}

// What reading a directory came to.
interface Read {
  data?: Listing;
  error: Error | null;
  isPending: boolean;
}

// rowsOf lays the open directories out as the rows of the tree.
function rowsOf(read: Map<string, Read>, open: ReadonlySet<string>): Row[] {
  const rows: Row[] = [];
  const walk = (directory: string, depth: number) => {
    const listing = read.get(directory);
    if (!listing || listing.isPending) {
      rows.push({ is: "wait", path: directory, depth });
      return;
    }
    if (listing.error || !listing.data) {
      const why = listing.error?.message ?? "";
      rows.push({ is: "fail", path: directory, depth, why });
      return;
    }
    if (!listing.data.entries.length && directory) {
      rows.push({ is: "none", path: directory, depth });
    }
    for (const entry of listing.data.entries) {
      const path = join(directory, entry.name);
      const opened = entry.type === "directory" && open.has(path);
      rows.push({ is: "entry", path, depth, entry, open: opened });
      if (opened) walk(path, depth + 1);
    }
    if (listing.data.next) rows.push({ is: "more", path: directory, depth });
  };
  walk("", 0);
  return rows;
}

// Tree is the files of a session's workspace as a tree, with what puts
// files into it and takes them out. A file opens when it is chosen. pulse
// changes when the agent of the session has done something, after which the
// tree is read again.
export function Tree({
  session,
  shown,
  writes,
  pulse,
  onClose,
}: {
  session: Session;
  shown: Shown;
  // Whether the caller may put files into the workspace.
  writes: boolean;
  pulse: string;
  onClose: () => void;
}) {
  const sid = session.id;
  // The directories that the tree shows: those that are open, and whose
  // directories above are open too.
  const paths = useMemo(
    () => [
      "",
      ...[...shown.open]
        .filter((path) => above(path).every((over) => shown.open.has(over)))
        .sort(),
    ],
    [shown.open],
  );
  const listings = useListings(sid, paths, true);
  const read = new Map<string, Read>(
    paths.map((path, index) => [path, listings[index]]),
  );
  const rows = rowsOf(read, shown.open);
  const root = read.get("")!;

  // What the directories told of their entries is kept for the files that
  // are open.
  useEffect(() => {
    for (const [directory, listing] of read) {
      for (const entry of listing.data?.entries ?? []) {
        note(sid, join(directory, entry.name), entry);
      }
    }
  });

  // The tree follows what the agent does, a moment after it did it.
  const first = useRef(true);
  useEffect(() => {
    if (first.current) {
      first.current = false;
      return;
    }
    const later = setTimeout(() => void refresh(sid), 400);
    return () => clearTimeout(later);
  }, [sid, pulse]);

  const [words, setWords] = useState("");
  const [sought, setSought] = useState("");
  useEffect(() => {
    const later = setTimeout(() => setSought(words.trim()), 250);
    return () => clearTimeout(later);
  }, [words]);

  const scroller = useRef<HTMLDivElement>(null);
  // The React Compiler, which the web UI is not built with, cannot memoize
  // what the virtualizer returns.
  // oxlint-disable-next-line react/incompatible-library
  const rowsShown = useVirtualizer({
    count: rows.length,
    getScrollElement: () => scroller.current,
    estimateSize: () => ROW,
    overscan: 16,
  });

  // The row that the keys move from.
  const [cursor, setCursor] = useState<string>();
  const at = rows.findIndex((row) => row.is === "entry" && row.path === cursor);

  function choose(row: Row) {
    if (row.is === "more") return void more(sid, row.path).catch(oops);
    if (row.is === "fail") return void refresh(sid);
    if (row.is !== "entry" || !row.entry) return;
    setCursor(row.path);
    if (row.entry.type === "directory") toggle(sid, row.path);
    else if (row.entry.type === "file") openFile(sid, row.path);
  }

  function keyed(event: KeyboardEvent<HTMLDivElement>) {
    const go = (index: number) => {
      const next = rows[Math.max(0, Math.min(rows.length - 1, index))];
      if (!next) return;
      setCursor(next.is === "entry" ? next.path : cursor);
      rowsShown.scrollToIndex(rows.indexOf(next));
    };
    const row = rows[at];
    const keys: Record<string, () => void> = {
      ArrowDown: () => go(at + 1),
      ArrowUp: () => go(at < 0 ? 0 : at - 1),
      Home: () => go(0),
      End: () => go(rows.length - 1),
      ArrowRight: () => {
        if (row?.entry?.type !== "directory") return;
        if (row.open) go(at + 1);
        else toggle(sid, row.path);
      },
      ArrowLeft: () => {
        if (!row) return;
        if (row.open) return toggle(sid, row.path);
        const over = parentOf(row.path);
        if (row.depth > 0) go(rows.findIndex((each) => each.path === over));
      },
      Enter: () => row && choose(row),
      " ": () => row && choose(row),
    };
    const act = keys[event.key];
    if (!act) return;
    event.preventDefault();
    act();
  }

  // What is being saved, by path.
  const [saving, setSaving] = useState<ReadonlySet<string>>(new Set());
  async function save(path: string, entry?: Entry) {
    setSaving((was) => new Set(was).add(path));
    try {
      if (entry && entry.type !== "directory") {
        saveFile(entry.name, await readFile(sid, path));
      } else {
        const name = path ? nameOf(path) : "workspace";
        saveFile(name + ".zip", await readArchive(sid, path));
      }
    } catch (error) {
      oops(error);
    }
    setSaving((was) => {
      const left = new Set(was);
      left.delete(path);
      return left;
    });
  }

  // Putting files into the workspace.
  const limits = useLimits().data;
  const batch = useBatch(sid);
  const files = useRef<HTMLInputElement>(null);
  const folder = useRef<HTMLInputElement>(null);
  // The directory that the files that are picked go into.
  const into = useRef("");
  // The files that would replace what is there, until a person says so.
  const [asking, setAsking] = useState<{
    directory: string;
    picked: Picked[];
    taken: string[];
  }>();
  const done = (sent: Batch) => {
    if (!sent.failed) toast(`${plural(sent.done, "file")} uploaded`, "ok");
  };
  function begin(directory: string, picked: Picked[]) {
    reveal(sid, directory, true);
    upload(sid, directory, picked, done);
  }
  async function put(directory: string, all: Picked[]) {
    const most = limits?.max_file_bytes ?? Infinity;
    const picked = all.filter(({ file }) => file.size <= most);
    if (picked.length < all.length) {
      toast(
        `${plural(all.length - picked.length, "file")} over ${size(most)} left out`,
        "bad",
        5200,
      );
    }
    if (!picked.length) return;
    try {
      const there = await readNames(sid, directory);
      const taken = tops(picked).filter((name) => there.has(name));
      if (taken.length) setAsking({ directory, picked, taken });
      else begin(directory, picked);
    } catch (error) {
      oops(error);
    }
  }
  function pick(directory: string, what: "files" | "folder") {
    into.current = directory;
    (what === "files" ? files : folder).current?.click();
  }

  // The directory that what is dragged over the tree would go into.
  const [over, setOver] = useState<string>();
  const carries = (event: DragEvent) =>
    writes && event.dataTransfer.types.includes("Files");
  function dragged(event: DragEvent, directory: string) {
    if (!carries(event)) return;
    event.preventDefault();
    event.stopPropagation();
    event.dataTransfer.dropEffect = "copy";
    setOver(directory);
  }
  function dropped(event: DragEvent, directory: string) {
    if (!carries(event)) return;
    event.preventDefault();
    event.stopPropagation();
    setOver(undefined);
    fromDrop(event.dataTransfer)
      .then((picked) => put(directory, picked))
      .catch(oops);
  }

  const found = useSearch(sid, sought);
  const refused =
    root.error instanceof ApiError && root.error.code === "no_room";
  const none = !root.isPending && !root.error && !rows.length;

  return (
    <div
      className={cls("ft", over === "" && "drop")}
      onDragOver={(event) => dragged(event, "")}
      onDragLeave={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget as Node))
          setOver(undefined);
      }}
      onDrop={(event) => dropped(event, "")}
    >
      <div className="r-top">
        <h2 className="h3" title={ROOT}>
          Files
        </h2>
        <span className="ft-acts">
          {writes && (
            <Menu
              align="right"
              button={(props) => (
                <button
                  className="ib sm"
                  type="button"
                  title="Upload"
                  {...props}
                >
                  <Icon name="upload" />
                </button>
              )}
            >
              <MenuItem
                glyph="file"
                text="Upload files…"
                onClick={() => pick("", "files")}
              />
              <MenuItem
                glyph="folder"
                text="Upload a folder…"
                onClick={() => pick("", "folder")}
              />
            </Menu>
          )}
          <button
            className="ib sm"
            type="button"
            title="Download all as ZIP"
            disabled={saving.has("") || none}
            onClick={() => void save("")}
          >
            {saving.has("") ? (
              <span className="ring" />
            ) : (
              <Icon name="download" />
            )}
          </button>
          <button
            className="ib sm"
            type="button"
            title="Refresh"
            onClick={() => void refresh(sid)}
          >
            <Icon name="refresh" />
          </button>
          <button
            className="ib sm"
            type="button"
            title="Close"
            onClick={onClose}
          >
            <Icon name="chev" />
          </button>
        </span>
      </div>
      <label className="ft-find">
        <Icon name="search" />
        <input
          type="search"
          placeholder="Find a file"
          aria-label="Find a file by name"
          value={words}
          onChange={(event) => setWords(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Escape") setWords("");
          }}
        />
        {!sought && shown.open.size > 0 && (
          <button
            className="ib sm"
            type="button"
            title="Collapse all"
            onClick={() => collapse(sid)}
          >
            <Icon name="updown" />
          </button>
        )}
      </label>
      {sought ? (
        <div className="ft-s">
          {found.isPending ? (
            <div className="ft-pad">
              <Loading lines={3} />
            </div>
          ) : found.error ? (
            <p className="ft-note">{sentence(found.error.message)}</p>
          ) : !found.data.entries.length ? (
            <p className="ft-note">Nothing has “{sought}” in its name.</p>
          ) : (
            <ul className="ft-found" aria-label="Files found">
              {found.data.entries.map((entry) => (
                <li key={entry.path}>
                  <button
                    type="button"
                    onClick={() => {
                      note(sid, entry.path, entry);
                      reveal(sid, entry.path, entry.type === "directory");
                      if (entry.type === "file") openFile(sid, entry.path);
                      else {
                        setWords("");
                        setCursor(entry.path);
                      }
                    }}
                  >
                    <Mark entry={entry} />
                    <span className="nm">{entry.name}</span>
                    <small>{parentOf(entry.path)}</small>
                  </button>
                </li>
              ))}
              {found.data.truncated && (
                <li className="ft-note">
                  The first {found.data.entries.length} are shown.
                </li>
              )}
            </ul>
          )}
        </div>
      ) : root.isPending ? (
        <div className="ft-s ft-pad">
          <Loading lines={5} />
        </div>
      ) : root.error ? (
        <div className="ft-s">
          <Empty
            glyph={refused ? "moon" : "warn"}
            title={
              refused
                ? "No worker has room for the sandbox"
                : "Could not read the files"
            }
            text={refused ? undefined : sentence(root.error.message)}
            action={
              <button
                className="btn sec sm"
                type="button"
                onClick={() => void refresh(sid)}
              >
                Try again
              </button>
            }
          />
        </div>
      ) : none ? (
        <div className="ft-s">
          <Empty
            glyph="folder"
            title="No files yet"
            action={
              writes && (
                <button
                  className="btn sec sm"
                  type="button"
                  onClick={() => pick("", "files")}
                >
                  <Icon name="upload" />
                  Upload files
                </button>
              )
            }
          />
        </div>
      ) : (
        <div
          ref={scroller}
          className="ft-s"
          role="tree"
          tabIndex={0}
          aria-label={"Files of " + ROOT}
          aria-activedescendant={at < 0 ? undefined : "ft-" + at}
          onKeyDown={keyed}
        >
          <div
            style={{ height: rowsShown.getTotalSize(), position: "relative" }}
          >
            {rowsShown.getVirtualItems().map((item) => {
              const row = rows[item.index];
              const place = {
                transform: `translateY(${item.start}px)`,
                paddingLeft: 10 + row.depth * 14 + "px",
              };
              if (row.is !== "entry" || !row.entry) {
                return (
                  <div
                    key={row.is + row.path}
                    className={cls("ft-row", "ft-" + row.is)}
                    style={place}
                    onClick={() => choose(row)}
                  >
                    {row.is === "wait" ? (
                      <span className="skel" />
                    ) : row.is === "more" ? (
                      "Show more"
                    ) : row.is === "none" ? (
                      "Empty"
                    ) : (
                      <span title={sentence(row.why ?? "")}>
                        Could not be read. Try again
                      </span>
                    )}
                  </div>
                );
              }
              const entry = row.entry;
              const directory = entry.type === "directory";
              const target = directory ? row.path : parentOf(row.path);
              return (
                <div
                  key={row.path}
                  id={"ft-" + item.index}
                  className={cls(
                    "ft-row",
                    row.path === shown.at && "on",
                    row.path === cursor && "cur",
                    directory && over === row.path && "drop",
                    entry.name.startsWith(".") && "dim",
                  )}
                  style={place}
                  role="treeitem"
                  aria-level={row.depth + 1}
                  aria-expanded={directory ? !!row.open : undefined}
                  aria-selected={row.path === shown.at}
                  title={
                    entry.target
                      ? `${entry.name} → ${entry.target}`
                      : entry.name
                  }
                  onClick={() => choose(row)}
                  onDragOver={(event) => dragged(event, target)}
                  onDrop={(event) => dropped(event, target)}
                >
                  {directory ? (
                    <Icon
                      name="chev"
                      className={cls("tw", row.open && "open")}
                    />
                  ) : (
                    <span className="tw" />
                  )}
                  <Mark entry={entry} />
                  <span className="nm">{entry.name}</span>
                  {entry.target && <Icon name="out" className="lk" />}
                  <span
                    className="ft-do"
                    onClick={(event) => event.stopPropagation()}
                  >
                    {saving.has(row.path) ? (
                      <span className="ring" />
                    ) : (
                      <Menu
                        align="right"
                        button={(props) => (
                          <button
                            className="ib xs"
                            type="button"
                            title="More"
                            tabIndex={-1}
                            {...props}
                          >
                            <Icon name="dots" />
                          </button>
                        )}
                      >
                        {directory && writes && (
                          <>
                            <MenuItem
                              glyph="file"
                              text="Upload files here…"
                              onClick={() => pick(row.path, "files")}
                            />
                            <MenuItem
                              glyph="folder"
                              text="Upload a folder here…"
                              onClick={() => pick(row.path, "folder")}
                            />
                            <hr />
                          </>
                        )}
                        {entry.type !== "symlink" && (
                          <MenuItem
                            glyph="download"
                            text={directory ? "Download as ZIP" : "Download"}
                            onClick={() => void save(row.path, entry)}
                          />
                        )}
                        <MenuItem
                          glyph="copy"
                          text="Copy the path"
                          onClick={() =>
                            void copy(`${ROOT}/${row.path}`, "path")
                          }
                        />
                      </Menu>
                    )}
                  </span>
                </div>
              );
            })}
          </div>
        </div>
      )}
      {batch && <Sending session={sid} batch={batch} onDone={done} />}
      {writes && (
        <>
          <input
            ref={files}
            type="file"
            multiple
            hidden
            onChange={(event) => {
              const picked = fromInput(event.target.files!);
              event.target.value = "";
              void put(into.current, picked);
            }}
          />
          <input
            ref={folder}
            type="file"
            hidden
            // @ts-expect-error Browsers pick a folder with this attribute.
            webkitdirectory=""
            onChange={(event) => {
              const picked = fromInput(event.target.files!);
              event.target.value = "";
              void put(into.current, picked);
            }}
          />
        </>
      )}
      {asking && (
        <Dialog
          size="slim"
          danger
          title={
            asking.taken.length === 1
              ? `Replace “${asking.taken[0]}”?`
              : `Replace ${asking.taken.length} items?`
          }
          lead={
            (asking.taken.length > 1
              ? asking.taken.slice(0, 4).join(", ") +
                (asking.taken.length > 4 ? "…" : "") +
                " are"
              : "It is") +
            ` already in ${asking.directory ? nameOf(asking.directory) : "the workspace"}.`
          }
          action="Replace"
          onSubmit={() => true}
          onClose={(replace) => {
            if (replace) begin(asking.directory, asking.picked);
            setAsking(undefined);
          }}
        />
      )}
    </div>
  );
}

// Mark is the small picture of what an entry is.
function Mark({ entry }: { entry: Entry }) {
  if (entry.type === "directory") {
    return <Icon name="folder" className="mk dir" />;
  }
  const { kind, glyph } = sortOf(entry.name);
  return <Icon name={glyph} className={"mk " + kind.family} />;
}

// Sending says how far the files that are put into the workspace have gone,
// and gives them up or sends again those that failed.
function Sending({
  session,
  batch,
  onDone,
}: {
  session: string;
  batch: Batch;
  onDone: (batch: Batch) => void;
}) {
  const gone = batch.bytes ? Math.round((batch.sent / batch.bytes) * 100) : 0;
  // What has gone without a failure needs no saying once it is done.
  useEffect(() => {
    if (!batch.going && !batch.failed) dismiss(session);
  }, [session, batch.going, batch.failed]);
  if (!batch.going && !batch.failed) return null;
  return (
    <div className={cls("ft-up", !batch.going && "bad")} role="status">
      <span className="tx">
        {batch.going ? (
          <>
            <b>
              Uploading{" "}
              {batch.done + 1 > batch.total ? batch.total : batch.done + 1} of{" "}
              {batch.total}
            </b>
            <small>
              {size(batch.sent)} of {size(batch.bytes)}
            </small>
          </>
        ) : (
          <>
            <b>{plural(batch.failed, "file")} failed</b>
            <small title={sentence(batch.why ?? "")}>
              {batch.why
                ? sentence(batch.why)
                : `${batch.done} of ${batch.total} uploaded`}
            </small>
          </>
        )}
      </span>
      {!batch.going && (
        <button
          className="btn sec sm"
          type="button"
          onClick={() => retry(session, onDone)}
        >
          Retry
        </button>
      )}
      <button
        className="ib sm"
        type="button"
        title={batch.going ? "Cancel" : "Dismiss"}
        onClick={() => dismiss(session)}
      >
        <Icon name="x" />
      </button>
      {batch.going && <i className="gone" style={{ width: gone + "%" }} />}
    </div>
  );
}
