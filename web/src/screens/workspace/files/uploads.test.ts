// @vitest-environment happy-dom
import { act, renderHook } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import {
  type Batch,
  dismiss,
  fromInput,
  retry,
  tops,
  upload,
  useBatch,
} from "./uploads.ts";

// A file on its way: where it goes, and what ends its request.
interface Put {
  path: string;
  file: File;
  signal: AbortSignal;
  progress: (sent: number) => void;
  arrive: () => void;
  fail: (why: Error) => void;
}

const { puts, refresh } = vi.hoisted(() => ({
  puts: [] as Put[],
  refresh: vi.fn(() => Promise.resolve()),
}));

vi.mock("../../../api/workspace.ts", () => ({
  refresh,
  putFile: (
    _session: string,
    path: string,
    file: File,
    options: { signal: AbortSignal; onProgress: (sent: number) => void },
  ) =>
    new Promise<void>((arrive, fail) => {
      puts.push({
        path,
        file,
        signal: options.signal,
        progress: options.onProgress,
        arrive,
        fail,
      });
    }),
}));

afterEach(() => {
  dismiss("s1");
  puts.length = 0;
  refresh.mockClear();
});

const picked = (...paths: string[]) =>
  paths.map((path) => ({ path, file: new File(["1234"], path) }));

// settle lets what follows the end of a request happen.
const settle = () => act(() => new Promise((resolve) => setTimeout(resolve)));

it("names what files take in the directory that they are put into", () => {
  expect(tops(picked("a.txt", "src/b.ts", "src/deep/c.ts", "a.txt"))).toEqual([
    "a.txt",
    "src",
  ]);
  const inside = new File(["a"], "a.txt");
  Object.defineProperty(inside, "webkitRelativePath", {
    value: "bundle/deep/a.txt",
  });
  const files = [new File(["b"], "b.txt"), inside] as unknown as FileList;
  expect(fromInput(files).map(({ path }) => path)).toEqual([
    "b.txt",
    "bundle/deep/a.txt",
  ]);
});

it("sends a few files at a time, each to its place, and tells how far they are", async () => {
  const { result } = renderHook(() => useBatch("s1"));
  const done = vi.fn<(batch: Batch) => void>();
  act(() =>
    upload("s1", "docs", picked("a.txt", "b.txt", "in/c.txt", "d.txt"), done),
  );
  expect(puts.map((put) => put.path)).toEqual([
    "docs/a.txt",
    "docs/b.txt",
    "docs/in/c.txt",
  ]);
  expect(result.current).toMatchObject({
    total: 4,
    done: 0,
    failed: 0,
    bytes: 16,
    sent: 0,
    going: true,
  });
  act(() => puts[1].progress(3));
  expect(result.current!.sent).toBe(3);
  // The next file goes once one has arrived.
  puts[0].arrive();
  await settle();
  expect(puts.map((put) => put.path).at(-1)).toBe("docs/d.txt");
  expect(result.current).toMatchObject({ done: 1, sent: 7, going: true });
  // More files join what is going.
  act(() => upload("s1", "", picked("e.txt"), done));
  expect(result.current!.total).toBe(5);
  for (const put of puts.slice(1)) put.arrive();
  await settle();
  puts.at(-1)!.arrive();
  await settle();
  expect(puts.map((put) => put.path).at(-1)).toBe("e.txt");
  expect(result.current).toMatchObject({
    total: 5,
    done: 5,
    failed: 0,
    sent: 20,
    going: false,
  });
  expect(done).toHaveBeenCalledTimes(1);
  expect(refresh).toHaveBeenCalledWith("s1");
});

it("sends again only the files that failed", async () => {
  const { result } = renderHook(() => useBatch("s1"));
  const done = vi.fn<(batch: Batch) => void>();
  act(() => upload("s1", "docs", picked("a.txt", "b.txt"), done));
  puts[0].arrive();
  puts[1].fail(new Error("the sandbox has no room for the file"));
  await settle();
  expect(result.current).toMatchObject({
    done: 1,
    failed: 1,
    going: false,
    why: "the sandbox has no room for the file",
  });
  act(() => retry("s1", done));
  expect(puts.map((put) => put.path)).toEqual([
    "docs/a.txt",
    "docs/b.txt",
    "docs/b.txt",
  ]);
  expect(result.current).toMatchObject({ total: 1, done: 0, going: true });
  puts[2].arrive();
  await settle();
  expect(result.current).toMatchObject({ done: 1, failed: 0, going: false });
});

it("gives up the files that have not gone", async () => {
  const { result } = renderHook(() => useBatch("s1"));
  const done = vi.fn<(batch: Batch) => void>();
  act(() => upload("s1", "", picked("a.txt", "b.txt", "c.txt", "d.txt"), done));
  act(() => dismiss("s1"));
  expect(result.current).toBeUndefined();
  expect(puts.every((put) => put.signal.aborted)).toBe(true);
  // What ends afterwards starts nothing, and is not counted.
  for (const put of puts) put.fail(new DOMException("given up", "AbortError"));
  await settle();
  expect(puts.length).toBe(3);
  expect(done).not.toHaveBeenCalled();
  expect(result.current).toBeUndefined();
});
