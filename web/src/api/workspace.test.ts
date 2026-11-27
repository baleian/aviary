import { afterEach, expect, it, vi } from "vitest";
import { api } from "./client.ts";
import { readNames } from "./workspace.ts";

afterEach(() => vi.restoreAllMocks());

it("reads the names of a whole directory, a part at a time", async () => {
  const entry = (name: string) => ({
    name,
    type: "file",
    modified_at: "2026-11-27T00:00:00Z",
  });
  const get = vi
    .spyOn(api, "get")
    .mockResolvedValueOnce({
      entries: [entry("a b.txt"), entry("c.txt")],
      next: "f/c.txt",
    })
    .mockResolvedValueOnce({ entries: [entry("d.txt")] });
  expect([...(await readNames("s1", "docs/2026"))]).toEqual([
    "a b.txt",
    "c.txt",
    "d.txt",
  ]);
  expect(get.mock.calls.map(([path]) => path)).toEqual([
    "/v1/sessions/s1/files?path=docs%2F2026&limit=1000",
    "/v1/sessions/s1/files?path=docs%2F2026&after=f%2Fc.txt&limit=1000",
  ]);
});
