import { describe, expect, it } from "vitest";
import {
  changed,
  exitCode,
  fieldsOf,
  findDiff,
  inputOf,
  outputText,
  outsideOf,
  pathOf,
} from "./tools.ts";

describe("findDiff", () => {
  const diff = "--- a/x\n+++ b/x\n@@ -1 +1 @@\n-old\n+new";

  it("finds a unified diff, also inside the fields of a tool", () => {
    expect(findDiff(diff)).toBe(diff);
    expect(findDiff({ metadata: { diff } })).toBe(diff);
    expect(findDiff({ content: [{ text: diff }] })).toBe(diff);
  });

  it("does not take other text for one", () => {
    expect(findDiff("- a list\n- of things")).toBeNull();
    expect(findDiff("@@ only a header")).toBeNull();
    expect(findDiff({ output: "done" })).toBeNull();
    expect(findDiff(null)).toBeNull();
  });
});

describe("changed", () => {
  it("makes a diff of the text before and after an edit", () => {
    expect(changed({ oldString: "a\nb", newString: "a\nc" })).toBe(
      "@@\n- a\n- b\n+ a\n+ c",
    );
    expect(changed({ old_str: "", new_str: "x" })).toBe("@@\n+ x");
  });

  it("makes none of other input", () => {
    expect(changed({ command: "ls" })).toBeNull();
    expect(changed({ old_string: "a" })).toBeNull();
  });
});

describe("outputText", () => {
  it("is the text of the output, in whichever field it is", () => {
    expect(outputText("plain")).toBe("plain");
    expect(outputText({ stdout: "out", stderr: "err" })).toBe("out");
    expect(outputText({ output: "", aggregated_output: "all" })).toBe("all");
    expect(outputText({ error: "refused" })).toBe("refused");
  });

  it("is empty for output without text", () => {
    expect(outputText({ exit_code: 0 })).toBe("");
    expect(outputText(undefined)).toBe("");
  });
});

it("reads the exit code of a command", () => {
  expect(exitCode({ exit_code: 2 })).toBe(2);
  expect(exitCode({ metadata: { exit: 0 } })).toBe(0);
  expect(exitCode({ exit_code: "2" })).toBeNull();
  expect(exitCode(undefined)).toBeNull();
});

it("reads the file that a tool call is about", () => {
  expect(pathOf({ filePath: "a.ts" })).toBe("a.ts");
  expect(pathOf({ file_path: "b.ts" })).toBe("b.ts");
  expect(pathOf({ path: "c.ts" })).toBe("c.ts");
  expect(pathOf({ path: 7 })).toBe("");
  expect(inputOf("text")).toEqual({});
  expect(inputOf({ command: "ls" })).toEqual({ command: "ls" });
});

it("reads the folders outside the workspace that a tool call reaches", () => {
  expect(outsideOf({ directories: ["/tmp", 7, "/var"] })).toEqual([
    "/tmp",
    "/var",
  ]);
  expect(outsideOf({ directories: "/tmp" })).toEqual([]);
  expect(outsideOf({ command: "ls" })).toEqual([]);
});

it("reads the plain fields of an input that are not shown as what they are", () => {
  expect(
    fieldsOf({
      pattern: "retry",
      path: "/home/workspace/src",
      limit: 20,
      literal: false,
      note: "",
      nothing: null,
      globs: ["*.ts"],
      options: { deep: true },
    }),
  ).toEqual([
    ["pattern", "retry"],
    ["limit", "20"],
    ["literal", "false"],
  ]);
  // The command, the file and the text of an edit have places of their own.
  expect(
    fieldsOf({
      command: "ls",
      cwd: "/home",
      filePath: "a",
      file_path: "a",
      oldString: "a",
      old_string: "a",
      old_str: "a",
      newString: "b",
      new_string: "b",
      new_str: "b",
    }),
  ).toEqual([]);
  const many = Object.fromEntries(
    "abcdefgh".split("").map((name) => [name, 1]),
  );
  expect(fieldsOf(many).map(([name]) => name)).toEqual("abcdef".split(""));
});
