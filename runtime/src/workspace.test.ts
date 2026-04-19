import { after, before, describe, it } from "node:test";
import assert from "node:assert/strict";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";

// Must set WORKSPACE_ROOT before importing the modules under test — the
// constants module reads it at import time.
const tmpRoot = fs.mkdtempSync(path.join(os.tmpdir(), "aviary-ws-"));
process.env.WORKSPACE_ROOT = tmpRoot;

const { WorkspaceError, listTree, readFile } = await import("./workspace.js");

function sessionDir(sid: string): string {
  return path.join(tmpRoot, "sessions", sid, "shared");
}

function writeFile(sid: string, rel: string, content: string | Buffer): void {
  const abs = path.join(sessionDir(sid), rel);
  fs.mkdirSync(path.dirname(abs), { recursive: true });
  fs.writeFileSync(abs, content);
}

after(() => {
  if (fs.existsSync(tmpRoot)) {
    fs.rmSync(tmpRoot, { recursive: true, force: true });
  }
});

describe("workspace.listTree", () => {
  const sid = "sess-list";

  before(() => {
    fs.mkdirSync(sessionDir(sid), { recursive: true });
    writeFile(sid, "README.md", "hi");
    writeFile(sid, "b-file.txt", "x");
    fs.mkdirSync(path.join(sessionDir(sid), "src"), { recursive: true });
    fs.mkdirSync(path.join(sessionDir(sid), ".claude"), { recursive: true });
    fs.mkdirSync(path.join(sessionDir(sid), ".venv"), { recursive: true });
    writeFile(sid, ".gitignore", "node_modules/");
  });

  it("returns 1-depth entries sorted with dirs first", () => {
    const tree = listTree(sid, "/", false);
    assert.equal(tree.path, "/");
    const visible = tree.entries.map((e) => [e.type, e.name]);
    // .claude and .venv filtered; .gitignore (non-default-hidden) stays.
    // File order is case-insensitive locale-aware (matches VS Code Explorer).
    assert.deepEqual(visible, [
      ["dir", "src"],
      ["file", ".gitignore"],
      ["file", "b-file.txt"],
      ["file", "README.md"],
    ]);
  });

  it("surfaces default-hidden entries with hidden:true when requested", () => {
    const tree = listTree(sid, "/", true);
    const claudeEntry = tree.entries.find((e) => e.name === ".claude");
    const venvEntry = tree.entries.find((e) => e.name === ".venv");
    assert.equal(claudeEntry?.hidden, true);
    assert.equal(venvEntry?.hidden, true);
  });

  it("lists nested directories", () => {
    writeFile(sid, "src/app.ts", "console.log('hi');");
    const tree = listTree(sid, "/src", false);
    assert.equal(tree.path, "/src");
    assert.deepEqual(
      tree.entries.map((e) => e.name),
      ["app.ts"],
    );
  });

  it("auto-creates the session base dir so fresh sessions don't 404", () => {
    const freshSid = "sess-fresh";
    const tree = listTree(freshSid, "/", false);
    assert.deepEqual(tree.entries, []);
    assert.ok(fs.existsSync(sessionDir(freshSid)));
  });

  it("rejects path traversal", () => {
    assert.throws(
      () => listTree(sid, "../../etc", false),
      (err: unknown) => err instanceof WorkspaceError && err.code === "invalid_path",
    );
  });

  it("404s for a non-existent directory under the session", () => {
    assert.throws(
      () => listTree(sid, "/does-not-exist", false),
      (err: unknown) => err instanceof WorkspaceError && err.code === "not_found",
    );
  });

  it("refuses to traverse symlinks", () => {
    const linkSid = "sess-symlink";
    fs.mkdirSync(sessionDir(linkSid), { recursive: true });
    const outside = fs.mkdtempSync(path.join(os.tmpdir(), "aviary-ws-outside-"));
    try {
      fs.symlinkSync(outside, path.join(sessionDir(linkSid), "escape"));
      assert.throws(
        () => listTree(linkSid, "/escape", false),
        (err: unknown) => err instanceof WorkspaceError && err.code === "invalid_path",
      );
    } finally {
      fs.rmSync(outside, { recursive: true, force: true });
    }
  });
});

describe("workspace.readFile", () => {
  const sid = "sess-read";

  before(() => {
    fs.mkdirSync(sessionDir(sid), { recursive: true });
  });

  it("reads a text file", () => {
    writeFile(sid, "hello.txt", "hello world");
    const out = readFile(sid, "/hello.txt");
    assert.equal(out.content, "hello world");
    assert.equal(out.isBinary, false);
    assert.equal(out.size, 11);
    assert.equal(out.encoding, "utf8");
  });

  it("flags binary files without returning content", () => {
    writeFile(sid, "bin.dat", Buffer.from([0x00, 0x01, 0x02, 0x03]));
    const out = readFile(sid, "/bin.dat");
    assert.equal(out.isBinary, true);
    assert.equal(out.content, "");
  });

  it("rejects files over WORKSPACE_MAX_FILE_BYTES", () => {
    const prev = process.env.WORKSPACE_MAX_FILE_BYTES;
    process.env.WORKSPACE_MAX_FILE_BYTES = "8";
    try {
      writeFile(sid, "big.txt", "1234567890");
      assert.throws(
        () => readFile(sid, "/big.txt"),
        (err: unknown) => err instanceof WorkspaceError && err.code === "too_large",
      );
    } finally {
      if (prev === undefined) delete process.env.WORKSPACE_MAX_FILE_BYTES;
      else process.env.WORKSPACE_MAX_FILE_BYTES = prev;
    }
  });

  it("rejects a directory", () => {
    fs.mkdirSync(path.join(sessionDir(sid), "a-dir"), { recursive: true });
    assert.throws(
      () => readFile(sid, "/a-dir"),
      (err: unknown) => err instanceof WorkspaceError && err.code === "not_a_file",
    );
  });

  it("rejects traversal", () => {
    assert.throws(
      () => readFile(sid, "../../etc/passwd"),
      (err: unknown) => err instanceof WorkspaceError && err.code === "invalid_path",
    );
  });
});
