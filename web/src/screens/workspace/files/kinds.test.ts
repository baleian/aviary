import { expect, it } from "vitest";
import { isText, sortOf } from "./kinds.ts";

it("tells from its name how a file is looked at", () => {
  for (const [name, look, written, glyph] of [
    ["README.md", "markdown", true, "doc"],
    ["report.html", "page", true, "code"],
    ["chart.PNG", "image", false, "image"],
    ["architecture.svg", "image", true, "image"],
    ["design.pdf", "pdf", false, "doc"],
    ["total.ts", "code", false, "code"],
    ["errors.log", "code", false, "doc"],
    ["analysis.ipynb", "code", false, "code"],
    // What may be text has no look until it is read.
    ["Dockerfile", undefined, false, "file"],
    ["events.parquet", undefined, false, "file"],
    ["export.xlsx", undefined, false, "file"],
  ] as const) {
    const sort = sortOf(name);
    expect([sort.look, sort.written, sort.glyph], name).toEqual([
      look,
      written,
      glyph,
    ]);
  }
});

it("takes for text what has no byte that is zero", async () => {
  expect(await isText(new Blob(["FROM node:22\n"]))).toBe(true);
  expect(await isText(new Blob([""]))).toBe(true);
  expect(await isText(new Blob([new Uint8Array([80, 65, 82, 0, 49])]))).toBe(
    false,
  );
  // Only the beginning is looked at.
  const late = new Uint8Array(10000).fill(65);
  late[9000] = 0;
  expect(await isText(new Blob([late]))).toBe(true);
});
