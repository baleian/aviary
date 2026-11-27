// @vitest-environment happy-dom
import { act, renderHook } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import {
  above,
  beside,
  closeFiles,
  collapse,
  forget,
  join,
  nameOf,
  openFile,
  parentOf,
  reveal,
  setSource,
  toggle,
  useShown,
} from "./state.ts";

afterEach(forget);

it("tells the parts of a path apart", () => {
  expect(parentOf("src/cart/price.ts")).toBe("src/cart");
  expect(parentOf("README.md")).toBe("");
  expect(nameOf("src/cart/price.ts")).toBe("price.ts");
  expect(join("", "README.md")).toBe("README.md");
  expect(join("src", "index.ts")).toBe("src/index.ts");
  expect(above("src/cart/price.ts")).toEqual(["src", "src/cart"]);
  expect(above("README.md")).toEqual([]);
});

it("finds in the workspace what a document names beside itself", () => {
  for (const [address, path] of [
    ["architecture.svg", "docs/architecture.svg"],
    ["./img/a%20b.png", "docs/img/a b.png"],
    ["../README.md#develop", "README.md"],
    ["/src/index.ts?plain=1", "src/index.ts"],
    ["img//shot.png", "docs/img/shot.png"],
    // An address that is not written as one is taken as it is.
    ["100%.md", "docs/100%.md"],
  ]) {
    expect(beside("docs/guide.md", address), address).toBe(path);
  }
  // What is elsewhere, or outside the workspace, is not of the workspace.
  for (const address of [
    "https://example.com/a.png",
    "//example.com/a.png",
    "mailto:someone@example.com",
    "data:image/png;base64,AAAA",
    "#top",
    "../../etc/passwd",
    "",
    "..",
  ]) {
    expect(beside("docs/guide.md", address), address).toBeUndefined();
  }
});

it("keeps the files that are open in the order of their tabs", () => {
  const { result } = renderHook(() => useShown("s1"));
  const tabs = () => ({ tabs: result.current.tabs, at: result.current.at });
  act(() => {
    for (const path of ["a.md", "b.md", "c.md"]) openFile("s1", path);
    // A file that is open comes in view, and gets no second tab.
    openFile("s1", "a.md");
  });
  expect(tabs()).toEqual({ tabs: ["a.md", "b.md", "c.md"], at: "a.md" });
  // The tab after the one that closes comes in view, or else the last.
  act(() => closeFiles("s1", ["a.md"]));
  expect(tabs()).toEqual({ tabs: ["b.md", "c.md"], at: "b.md" });
  act(() => {
    openFile("s1", "c.md");
    closeFiles("s1", ["c.md"]);
  });
  expect(tabs()).toEqual({ tabs: ["b.md"], at: "b.md" });
  // A tab that is not in view closes without a change of the view.
  act(() => {
    openFile("s1", "d.md");
    openFile("s1", "b.md");
    closeFiles("s1", ["d.md"]);
  });
  expect(tabs()).toEqual({ tabs: ["b.md"], at: "b.md" });
  act(() => closeFiles("s1", ["b.md"]));
  expect(tabs()).toEqual({ tabs: [], at: undefined });
  // Another session has tabs of its own.
  act(() => openFile("s2", "x.md"));
  expect(tabs().tabs).toEqual([]);
});

it("opens the directories above what is revealed", () => {
  const { result } = renderHook(() => useShown("s1"));
  const open = () => [...result.current.open].sort();
  act(() => reveal("s1", "src/cart/price.ts"));
  expect(open()).toEqual(["src", "src/cart"]);
  act(() => reveal("s1", "docs/api", true));
  expect(open()).toEqual(["docs", "docs/api", "src", "src/cart"]);
  act(() => toggle("s1", "src"));
  expect(open()).toEqual(["docs", "docs/api", "src/cart"]);
  act(() => toggle("s1", "test"));
  expect(open()).toContain("test");
  act(() => collapse("s1"));
  expect(open()).toEqual([]);

  act(() => setSource("s1", "README.md", true));
  expect(result.current.source.has("README.md")).toBe(true);
  act(() => setSource("s1", "README.md", false));
  expect(result.current.source.has("README.md")).toBe(false);
});
