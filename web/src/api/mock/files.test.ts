import { expect, it } from "vitest";
// @ts-expect-error The mock is JavaScript, and has no types.
import { bytesOf } from "./files.js";

const made = (name: string, media_type: string, size: number): Promise<Blob> =>
  bytesOf({ name, media_type, size });

it("makes a file of the example data of its size, and the same each time", async () => {
  for (const [name, type, size] of [
    ["checkout-errors.log", "text/plain", 1843200],
    ["export.csv", "text/csv", 421888],
    ["plans.json", "application/json", 2048],
    ["notes.md", "text/markdown", 3187],
    ["README.txt", "text/plain", 740],
    ["board.xlsx", "application/octet-stream", 912384],
    ["design.pdf", "application/pdf", 2411724],
    ["flow.svg", "image/svg+xml", 1024],
    // A browser draws this one: the tests have nothing to draw with.
    ["image.png", "image/png", 96256],
  ] as const) {
    const file = await made(name, type, size);
    expect([name, file.size, file.type]).toEqual([name, size, type]);
    const again = await made(name, type, size);
    expect(await again.text()).toBe(await file.text());
  }
});

it("makes texts that read as what they are", async () => {
  const log = await (await made("errors.log", "text/plain", 400)).text();
  expect(log).toMatch(/^2026-11-24T23:\d\d:\d\d\.\d{3}Z (ERROR|WARN |INFO ) /);
  const csv = await (await made("export.csv", "text/csv", 400)).text();
  expect(csv.split("\n")[0]).toBe(
    "id,plan,region,started_at,cancelled_at,reason,monthly_cents",
  );
  expect(csv.split("\n")[1].split(",")).toHaveLength(7);
  const svg = await (await made("flow.svg", "image/svg+xml", 1024)).text();
  expect(svg.trim()).toMatch(/^<svg [^]*<\/svg>$/);
});

it("makes a PDF that says where its parts are", async () => {
  const pdf = await (await made("design.pdf", "application/pdf", 9000)).text();
  expect(pdf.startsWith("%PDF-1.4\n")).toBe(true);
  expect(pdf.endsWith("%%EOF\n")).toBe(true);
  // The table of the document names the place of each of its objects.
  const table = Number(/startxref\n(\d+)\n/.exec(pdf)![1]);
  expect(pdf.slice(table, table + 5)).toBe("xref\n");
  const places = [...pdf.slice(table).matchAll(/^(\d{10}) 00000 n $/gm)];
  expect(places.length).toBeGreaterThan(6);
  places.forEach(([, place], index) =>
    expect(pdf.slice(Number(place)).startsWith(`${index + 1} 0 obj\n`)).toBe(
      true,
    ),
  );
  expect(pdf).toContain("/Count 3");
});
