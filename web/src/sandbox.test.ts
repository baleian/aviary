import { expect, it, vi } from "vitest";
import frame from "../public/sandbox.html?raw";

// The script of the page in which the web UI shows a web page of a
// workspace, as the server sends it.
const script = /<script>([\s\S]*?)<\/script>/.exec(frame)![1];

it("shows in the frame only what the web UI that frames it passes on", () => {
  const parent = {};
  const written: string[] = [];
  const document = {
    open: vi.fn(),
    write: (page: string) => written.push(page),
    close: vi.fn(),
  };
  let hear: (event: unknown) => void = () => {};
  new Function("addEventListener", "parent", "location", "document", script)(
    (kind: string, listener: typeof hear) => {
      expect(kind).toBe("message");
      hear = listener;
    },
    parent,
    { origin: "https://aviary.example.com" },
    document,
  );
  const page = "<!doctype html><title>A report</title>";
  const said = { source: parent, origin: "https://aviary.example.com" };
  // Another page that has the frame, or that opened this page, is not heard.
  hear({ ...said, source: {}, data: { page } });
  hear({ ...said, origin: "https://elsewhere.example.com", data: { page } });
  hear({ ...said, origin: "null", data: { page } });
  // Nor is what is no page.
  hear({ ...said, data: { page: 1 } });
  hear({ ...said, data: null });
  expect(written).toEqual([]);
  expect(document.open).not.toHaveBeenCalled();

  hear({ ...said, data: { page } });
  expect(written).toEqual([page]);
  expect(document.open).toHaveBeenCalledTimes(1);
  expect(document.close).toHaveBeenCalledTimes(1);
});
