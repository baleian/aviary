/**
 * Session workspace browse — read-only directory listing + file read for the
 * Web UI's file-tree panel. Operates on the per-session shared dir
 * (`/workspace-root/sessions/{sid}/shared`) that every agent in the session
 * sees as `/workspace` inside bubblewrap.
 *
 * Security: every caller-supplied path is resolved against the session base
 * dir; results that escape that base (via `..`, absolute paths, or symlinks
 * pointing outside) are rejected. Runtime trusts the supervisor for auth.
 */

import * as fs from "node:fs";
import * as path from "node:path";

import { sessionSharedDir } from "./constants.js";

export const DEFAULT_HIDDEN_NAMES = new Set([".claude", ".venv", ".cache"]);

const DEFAULT_MAX_FILE_BYTES = 2 * 1024 * 1024; // 2 MiB

export function workspaceMaxFileBytes(): number {
  const raw = process.env.WORKSPACE_MAX_FILE_BYTES;
  const parsed = raw ? parseInt(raw, 10) : NaN;
  return Number.isFinite(parsed) && parsed > 0 ? parsed : DEFAULT_MAX_FILE_BYTES;
}

export interface TreeEntry {
  name: string;
  type: "file" | "dir";
  size?: number;
  mtime?: number;
  hidden?: boolean;
}

export interface TreeListing {
  path: string;
  entries: TreeEntry[];
}

export interface FileContents {
  path: string;
  content: string;
  encoding: "utf8" | "base64";
  size: number;
  mtime: number;
  isBinary: boolean;
  truncated: boolean;
}

export class WorkspaceError extends Error {
  constructor(public readonly code: "invalid_path" | "not_found" | "not_a_directory" | "not_a_file" | "too_large", message: string) {
    super(message);
    this.name = "WorkspaceError";
  }
}

/** Normalize a caller-supplied relative path. "/", "", ".", and "./" all mean
 *  the session root. Anything absolute is rebased — we treat `/foo/bar` and
 *  `foo/bar` the same. The returned value is always resolved strictly under
 *  `base`; traversal attempts throw WorkspaceError("invalid_path"). */
export function resolveInsideBase(base: string, rel: string): string {
  const stripped = (rel ?? "").replace(/^\/+/, "");
  const joined = path.resolve(base, stripped);
  const baseResolved = path.resolve(base);
  if (joined !== baseResolved && !joined.startsWith(baseResolved + path.sep)) {
    throw new WorkspaceError("invalid_path", "path escapes session workspace");
  }
  return joined;
}

/** 1-depth directory listing. Default-hidden names are filtered unless
 *  `includeHidden` is true, in which case they appear with `hidden: true`. */
export function listTree(
  sessionId: string,
  relPath: string,
  includeHidden: boolean,
): TreeListing {
  const base = sessionSharedDir(sessionId);
  // Ensure the session base exists — callers expect a clean empty tree on a
  // fresh session rather than 404.
  fs.mkdirSync(base, { recursive: true });

  const abs = resolveInsideBase(base, relPath);

  let stat: fs.Stats;
  try {
    stat = fs.lstatSync(abs);
  } catch {
    throw new WorkspaceError("not_found", "path not found");
  }
  if (stat.isSymbolicLink()) {
    throw new WorkspaceError("invalid_path", "symlinks are not traversed");
  }
  if (!stat.isDirectory()) {
    throw new WorkspaceError("not_a_directory", "path is not a directory");
  }

  const names = fs.readdirSync(abs);
  const entries: TreeEntry[] = [];
  for (const name of names) {
    const isHidden = DEFAULT_HIDDEN_NAMES.has(name);
    if (isHidden && !includeHidden) continue;

    let child: fs.Stats;
    try {
      child = fs.lstatSync(path.join(abs, name));
    } catch {
      continue;
    }
    if (child.isSymbolicLink()) continue;
    if (!child.isFile() && !child.isDirectory()) continue;

    const entry: TreeEntry = {
      name,
      type: child.isDirectory() ? "dir" : "file",
      mtime: Math.floor(child.mtimeMs),
    };
    if (child.isFile()) entry.size = child.size;
    if (isHidden) entry.hidden = true;
    entries.push(entry);
  }

  entries.sort((a, b) => {
    if (a.type !== b.type) return a.type === "dir" ? -1 : 1;
    return a.name.localeCompare(b.name);
  });

  const normalized = "/" + path.relative(base, abs).split(path.sep).filter(Boolean).join("/");
  return { path: normalized === "/" ? "/" : normalized, entries };
}

/** Read a single file under the session workspace. Binary files return
 *  `isBinary: true` with empty `content`. Files over the size cap are
 *  refused with `too_large`. */
export function readFile(sessionId: string, relPath: string): FileContents {
  const base = sessionSharedDir(sessionId);
  fs.mkdirSync(base, { recursive: true });

  const abs = resolveInsideBase(base, relPath);

  let stat: fs.Stats;
  try {
    stat = fs.lstatSync(abs);
  } catch {
    throw new WorkspaceError("not_found", "file not found");
  }
  if (stat.isSymbolicLink()) {
    throw new WorkspaceError("invalid_path", "symlinks are not followed");
  }
  if (!stat.isFile()) {
    throw new WorkspaceError("not_a_file", "path is not a file");
  }

  const max = workspaceMaxFileBytes();
  if (stat.size > max) {
    throw new WorkspaceError("too_large", `file exceeds ${max} bytes`);
  }

  const buf = fs.readFileSync(abs);
  const isBinary = looksBinary(buf);

  const normalized = "/" + path.relative(base, abs).split(path.sep).filter(Boolean).join("/");
  return {
    path: normalized,
    content: isBinary ? "" : buf.toString("utf8"),
    encoding: "utf8",
    size: stat.size,
    mtime: Math.floor(stat.mtimeMs),
    isBinary,
    truncated: false,
  };
}

// A NUL byte in the first ~8KB is the classic heuristic git/grep use to
// classify a file as binary. Good enough for "should the editor try to
// render this as text?" — callers that really need precision can re-check.
function looksBinary(buf: Buffer): boolean {
  const n = Math.min(buf.length, 8192);
  for (let i = 0; i < n; i++) {
    if (buf[i] === 0) return true;
  }
  return false;
}
