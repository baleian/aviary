import { expect, it } from "vitest";
import { kindOf, split } from "./files.ts";

it("tells what a file is from the extension of its name", () => {
  expect(kindOf("shot.PNG")).toEqual({
    ext: "png",
    family: "image",
    view: "image",
    type: "image/png",
  });
  expect(kindOf("photo.jpeg").type).toBe("image/jpeg");
  expect(kindOf("flow.svg").type).toBe("image/svg+xml");
  expect(kindOf("errors.log")).toEqual({
    ext: "log",
    family: "text",
    view: "text",
  });
  // A table that is text is shown as text.
  expect(kindOf("export.csv")).toEqual({
    ext: "csv",
    family: "sheet",
    view: "text",
  });
  // What the web UI cannot show has no view.
  expect(kindOf("design.pdf")).toEqual({ ext: "pdf", family: "pdf" });
  expect(kindOf("board.xlsx")).toEqual({ ext: "xlsx", family: "sheet" });
  expect(kindOf("deck.pptx")).toEqual({ ext: "pptx", family: "slides" });
  expect(kindOf("letter.docx")).toEqual({ ext: "docx", family: "doc" });
  expect(kindOf("call.mp4")).toEqual({ ext: "mp4", family: "media" });
  expect(kindOf("logs.tar.gz")).toEqual({ ext: "gz", family: "archive" });
  // A picture that browsers do not draw.
  expect(kindOf("IMG_0042.heic")).toEqual({ ext: "heic", family: "image" });
  expect(kindOf("data.bin")).toEqual({ ext: "bin", family: "other" });
});

it("goes by the media type where the name does not tell", () => {
  expect(kindOf("pasted", "image/png")).toEqual({
    ext: "",
    family: "image",
    view: "image",
    type: "image/png",
  });
  expect(kindOf("Makefile", "text/plain; charset=utf-8")).toEqual({
    ext: "",
    family: "text",
    view: "text",
  });
  expect(kindOf("report", "application/pdf")).toEqual({
    ext: "",
    family: "pdf",
  });
  expect(kindOf(".env", "application/octet-stream")).toEqual({
    ext: "",
    family: "other",
  });
  // The name is believed before the type: a picture is drawn as what its
  // name says, and a page that calls itself a picture is not drawn at all.
  expect(kindOf("page.html", "image/png")).toEqual({
    ext: "html",
    family: "text",
    view: "text",
  });
  expect(kindOf("shot.png", "text/html").type).toBe("image/png");
  // A type that is no picture's is not handed to the browser as one.
  expect(kindOf("odd", "image/x-unknown")).toEqual({
    ext: "",
    family: "other",
  });
});

it("splits a long name before its end, which tells it apart", () => {
  expect(split("image (2).png")).toEqual(["image (2).png", ""]);
  expect(split("notes.md")).toEqual(["notes.md", ""]);
  expect(split("cancellations by plan and region v3.xlsx")).toEqual([
    "cancellations by plan and region ",
    "v3.xlsx",
  ]);
  expect(split("churn-dashboard-2026-11-24-at-10.32.15.png")).toEqual([
    "churn-dashboard-2026-11-24-at-10.32",
    ".15.png",
  ]);
  expect(split("결제 웹훅 재시도 설계.pdf")).toEqual([
    "결제 웹훅 재시도 ",
    "설계.pdf",
  ]);
  // A name without an extension keeps its last letters.
  expect(split("a-very-long-name-without-any")).toEqual([
    "a-very-long-name-without-",
    "any",
  ]);
  // What follows a dot far from the end is no extension.
  expect(split("release.notes-for-everyone")).toEqual([
    "release.notes-for-every",
    "one",
  ]);
  // The two parts are the name.
  for (const name of ["a.b", ".gitignore", "x", "report final (3).tar.gz"]) {
    expect(split(name).join("")).toBe(name);
  }
});
