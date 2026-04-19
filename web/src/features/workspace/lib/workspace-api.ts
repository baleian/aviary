import { http } from "@/lib/http/client";
import { ApiError } from "@/lib/http/errors";

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

export type WorkspaceErrorCode =
  | "invalid_path"
  | "not_found"
  | "not_a_directory"
  | "not_a_file"
  | "too_large"
  | "stale"
  | "exists"
  | "not_empty";

export class WorkspaceApiError extends Error {
  constructor(
    public readonly status: number,
    public readonly code: WorkspaceErrorCode | null,
    message: string,
    public readonly currentMtime: number | null = null,
    public readonly currentSize: number | null = null,
  ) {
    super(message);
    this.name = "WorkspaceApiError";
  }
}

function asWorkspaceError(err: unknown): WorkspaceApiError | null {
  if (!(err instanceof ApiError)) return null;
  const detail = err.detail as Record<string, unknown> | undefined;
  const code = (detail?.code as WorkspaceErrorCode | undefined) ?? null;
  const message =
    (detail?.error as string | undefined) ??
    (detail?.detail as string | undefined) ??
    err.message;
  const currentMtime =
    typeof detail?.current_mtime === "number" ? detail.current_mtime : null;
  const currentSize =
    typeof detail?.current_size === "number" ? detail.current_size : null;
  return new WorkspaceApiError(err.status, code, message, currentMtime, currentSize);
}

async function run<T>(op: () => Promise<T>): Promise<T> {
  try {
    return await op();
  } catch (err) {
    const wrapped = asWorkspaceError(err);
    if (wrapped) throw wrapped;
    throw err;
  }
}

export async function getTree(
  sessionId: string,
  rel: string,
  includeHidden: boolean,
): Promise<TreeListing> {
  const params = new URLSearchParams({
    path: rel,
    include_hidden: includeHidden ? "true" : "false",
  });
  return run(() =>
    http.get<TreeListing>(
      `/sessions/${sessionId}/workspace/tree?${params.toString()}`,
    ),
  );
}

export async function getFile(sessionId: string, rel: string): Promise<FileContents> {
  const params = new URLSearchParams({ path: rel });
  return run(() =>
    http.get<FileContents>(`/sessions/${sessionId}/workspace/file?${params.toString()}`),
  );
}

export interface FileStat {
  path: string;
  size: number;
  mtime: number;
  isBinary: boolean;
}

export async function statFile(sessionId: string, rel: string): Promise<FileStat> {
  const params = new URLSearchParams({ path: rel });
  return run(() =>
    http.get<FileStat>(`/sessions/${sessionId}/workspace/stat?${params.toString()}`),
  );
}

export interface SaveFileArgs {
  content: string;
  encoding?: "utf8" | "base64";
  expectedMtime?: number | null;
  overwrite?: boolean;
}

export async function saveFile(
  sessionId: string,
  rel: string,
  args: SaveFileArgs,
): Promise<FileContents> {
  const body: Record<string, unknown> = {
    path: rel,
    content: args.content,
    encoding: args.encoding ?? "utf8",
    overwrite: !!args.overwrite,
  };
  if (args.expectedMtime != null) body.expected_mtime = args.expectedMtime;
  return run(() =>
    http.put<FileContents>(`/sessions/${sessionId}/workspace/file`, body),
  );
}

export async function createDir(sessionId: string, rel: string): Promise<void> {
  await run(() =>
    http.post<unknown>(`/sessions/${sessionId}/workspace/dir`, { path: rel }),
  );
}

export async function deleteEntry(
  sessionId: string,
  rel: string,
  recursive: boolean,
): Promise<void> {
  await run(() =>
    http.delete<unknown>(`/sessions/${sessionId}/workspace/entry`, {
      path: rel,
      recursive,
    }),
  );
}

export async function moveEntry(
  sessionId: string,
  from: string,
  to: string,
): Promise<void> {
  await run(() =>
    http.post<unknown>(`/sessions/${sessionId}/workspace/move`, { from, to }),
  );
}

function toBase64(buf: ArrayBuffer): string {
  const bytes = new Uint8Array(buf);
  let binary = "";
  const chunkSize = 0x8000;
  for (let i = 0; i < bytes.length; i += chunkSize) {
    binary += String.fromCharCode(
      ...bytes.subarray(i, Math.min(i + chunkSize, bytes.length)),
    );
  }
  return btoa(binary);
}

export const MAX_UPLOAD_BYTES = 10 * 1024 * 1024;

export async function uploadFile(
  sessionId: string,
  rel: string,
  file: File,
  overwrite: boolean,
): Promise<FileContents> {
  if (file.size > MAX_UPLOAD_BYTES) {
    throw new WorkspaceApiError(
      413,
      "too_large",
      `File exceeds ${MAX_UPLOAD_BYTES / (1024 * 1024)} MB upload limit`,
    );
  }
  const buf = await file.arrayBuffer();
  const content = toBase64(buf);
  return saveFile(sessionId, rel, { content, encoding: "base64", overwrite });
}

export function downloadFileUrl(
  sessionId: string,
  rel: string,
  opts?: { inline?: boolean },
): string {
  const params = new URLSearchParams({ path: rel });
  if (opts?.inline) params.set("inline", "true");
  return `/api/sessions/${sessionId}/workspace/download?${params.toString()}`;
}
