import { existsSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { expect, it } from "vitest";

// The font for Korean text is not bundled as the others are: its files are
// in public/, as its author distributes them, and the page links their
// stylesheet.
it("brings the font that the page links, with its license", () => {
  const page = readFileSync("index.html", "utf8");
  const linked = /href="(\/fonts\/[^"]+\.css)"/.exec(page);
  expect(linked).not.toBeNull();
  const sheet = join("public", linked![1]);
  const folder = dirname(sheet);
  expect(existsSync(join(folder, "LICENSE.txt"))).toBe(true);
  const files = [
    ...readFileSync(sheet, "utf8").matchAll(/url\(\.\/([^)]+)\)/g),
  ];
  expect(files.length).toBeGreaterThan(0);
  for (const [, file] of files) {
    expect(existsSync(join(folder, file)), file).toBe(true);
  }
});
