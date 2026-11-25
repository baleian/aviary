// @vitest-environment happy-dom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { ApiError } from "../../api/client.ts";
import * as conversation from "../../api/conversation.ts";
import { kindOf } from "../../lib/files.ts";
import * as pdf from "../../lib/pdf.ts";
import type { Shown } from "./shown.ts";
import { Viewer } from "./Viewer.tsx";

// The browser of the tests has no workers, which PDF.js reads with.
vi.mock("../../lib/pdf.ts", () => ({ open: vi.fn() }));

// The addresses that were made for files, and those that were let go of.
let made: Blob[] = [];
let revoked: string[] = [];

beforeEach(() => {
  made = [];
  revoked = [];
  vi.spyOn(URL, "createObjectURL").mockImplementation((file) => {
    made.push(file as Blob);
    return "blob:" + made.length;
  });
  vi.spyOn(URL, "revokeObjectURL").mockImplementation((address) => {
    revoked.push(address);
  });
  // Every page of a PDF is near what is shown.
  vi.stubGlobal(
    "IntersectionObserver",
    class {
      tell: IntersectionObserverCallback;
      constructor(tell: IntersectionObserverCallback) {
        this.tell = tell;
      }
      observe() {
        this.tell(
          [{ isIntersecting: true } as IntersectionObserverEntry],
          this as unknown as IntersectionObserver,
        );
      }
      disconnect() {}
    },
  );
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

// shown returns a file of the bytes, which may say that it is larger.
const shown = (name: string, bytes: BlobPart, size?: number): Shown => {
  const whole = new Blob([bytes]);
  return {
    key: name,
    name,
    size: size ?? whole.size,
    kind: kindOf(name),
    read: vi.fn(async (first?: number) => whole.slice(0, first)),
  };
};

function show(files: Shown[], at = 0, saves = false) {
  const onMove = vi.fn();
  const onClose = vi.fn();
  const view = render(
    <QueryClientProvider client={new QueryClient()}>
      <Viewer
        files={files}
        at={at}
        saves={saves}
        onMove={onMove}
        onClose={onClose}
      />
    </QueryClientProvider>,
  );
  const viewer = screen.getByRole<HTMLDialogElement>("dialog");
  const says = () => viewer.querySelector(".vw-t small")!.textContent;
  return { ...view, viewer, says, onMove, onClose };
}

it("shows a picture as what its name says it is, and at its own size after a click", async () => {
  const file = shown("shot.png", "the bytes of a picture");
  const { viewer, says, unmount } = show([file]);
  expect(viewer.open).toBe(true);
  expect(viewer.getAttribute("aria-label")).toBe("shot.png");
  // Until the file has come.
  expect(viewer.querySelector(".vw-b .ring")).toBeTruthy();
  const picture = await waitFor(() => {
    const found = viewer.querySelector<HTMLImageElement>(".vw-b img");
    expect(found).toBeTruthy();
    return found!;
  });
  expect(file.read).toHaveBeenCalledWith(undefined);
  expect(picture.getAttribute("src")).toBe("blob:1");
  expect([made[0].type, made[0].size]).toEqual(["image/png", 22]);
  expect(says()).toBe("22 bytes");

  // The picture says how large it is, and is larger than it is shown.
  Object.defineProperty(picture, "naturalWidth", { value: 1440 });
  Object.defineProperty(picture, "naturalHeight", { value: 900 });
  fireEvent.load(picture);
  expect(says()).toBe("22 bytes · 1440 × 900");
  expect(picture.className).toBe("shrunk");
  fireEvent.click(picture);
  expect(picture.className).toBe("whole");
  fireEvent.click(picture);
  expect(picture.className).toBe("shrunk");

  // The address of the file is let go of with the viewer.
  unmount();
  expect(revoked).toEqual(["blob:1"]);
});

it("shows the beginning of a text, and says if that is not all of it", async () => {
  const short = shown("notes.md", "# Notes\n\n한글도 있다.\n");
  const first = show([short]);
  await waitFor(() =>
    expect(first.viewer.querySelector(".vw-text pre")!.textContent).toBe(
      "# Notes\n\n한글도 있다.\n",
    ),
  );
  // No more than the first megabyte is read.
  expect(short.read).toHaveBeenCalledWith(1 << 20);
  expect(first.viewer.querySelector(".vw-text p")).toBeNull();
  cleanup();

  // A text that is cut within a character loses what is left of it.
  const cut = new TextEncoder().encode("에러").slice(0, 4);
  const long = shown("errors.log", cut, 5 << 20);
  const { viewer } = show([long]);
  await waitFor(() =>
    expect(viewer.querySelector(".vw-text pre")!.textContent).toBe("에"),
  );
  expect(viewer.querySelector(".vw-text p")!.textContent).toBe(
    "The first 4 bytes of 5 MB.",
  );
});

it("shows the pages of a PDF, and lets go of the document with the viewer", async () => {
  const drawn: number[] = [];
  const cancel = vi.fn();
  const pages = {
    count: 3,
    ratio: 0.7,
    draw: vi.fn((page: number) => {
      drawn.push(page);
      return { done: Promise.resolve(), cancel };
    }),
    close: vi.fn(),
  };
  vi.mocked(pdf.open).mockResolvedValue(pages);
  const file = shown("design.pdf", "%PDF-1.4");
  const { viewer, says, unmount } = show([file]);
  await waitFor(() =>
    expect(viewer.querySelectorAll(".vw-pages canvas")).toHaveLength(3),
  );
  expect(vi.mocked(pdf.open).mock.calls[0][0].size).toBe(8);
  expect(says()).toBe("8 bytes · 3 pages");
  // The pages that are near are drawn.
  await waitFor(() => expect(drawn).toEqual([1, 2, 3]));
  unmount();
  expect(cancel).toHaveBeenCalledTimes(3);
  expect(pages.close).toHaveBeenCalledTimes(1);
});

it("says that a file cannot be shown, or why it could not be read", async () => {
  // A file that is not the picture that its name says.
  const { viewer } = show([shown("shot.png", "not a picture")]);
  fireEvent.error(
    await waitFor(() => {
      const found = viewer.querySelector(".vw-b img");
      expect(found).toBeTruthy();
      return found!;
    }),
  );
  expect(viewer.querySelector(".vw-no")!.textContent).toBe(
    "This file cannot be shown.",
  );
  cleanup();

  vi.mocked(pdf.open).mockRejectedValue(new Error("Invalid PDF structure."));
  const broken = show([shown("design.pdf", "not a PDF")]);
  await waitFor(() =>
    expect(broken.viewer.querySelector(".vw-no")!.textContent).toBe(
      "This file cannot be shown.",
    ),
  );
  cleanup();

  // The API has the file no longer.
  const gone = shown("notes.txt", "");
  vi.mocked(gone.read).mockRejectedValue(
    new ApiError(404, { code: "not_found", message: "no such resource" }, {}),
  );
  const refused = show([gone]);
  await waitFor(() =>
    expect(refused.viewer.querySelector(".vw-no")!.textContent).toBe(
      "No such resource.",
    ),
  );
});

it("goes through the files with its arrows, and around at the ends", async () => {
  const user = userEvent.setup();
  const files = [shown("a.txt", "a"), shown("b.txt", "b"), shown("c.txt", "c")];
  const { viewer, onMove } = show(files, 2);
  expect(viewer.querySelector(".vw-n")!.textContent).toBe("3 / 3");
  await user.click(screen.getByRole("button", { name: "Next" }));
  expect(onMove).toHaveBeenLastCalledWith(0);
  await user.click(screen.getByRole("button", { name: "Previous" }));
  expect(onMove).toHaveBeenLastCalledWith(1);
  fireEvent.keyDown(viewer, { key: "ArrowRight" });
  expect(onMove).toHaveBeenLastCalledWith(0);
  fireEvent.keyDown(viewer, { key: "ArrowLeft" });
  expect(onMove).toHaveBeenLastCalledWith(1);
  cleanup();

  // A file that is alone has nowhere to go.
  const alone = show([shown("a.txt", "a")]);
  expect(alone.viewer.querySelector(".vw-n")).toBeNull();
  expect(screen.queryByRole("button", { name: "Next" })).toBeNull();
  fireEvent.keyDown(alone.viewer, { key: "ArrowRight" });
  expect(alone.onMove).not.toHaveBeenCalled();
});

it("closes with its button, with Escape and with a press beside the file", async () => {
  const user = userEvent.setup();
  const { viewer, onClose } = show([shown("a.txt", "a")]);
  await user.click(screen.getByRole("button", { name: "Close" }));
  expect(onClose).toHaveBeenCalledTimes(1);
  // The browser asks a dialog to close when Escape is pressed.
  fireEvent(viewer, new Event("cancel", { cancelable: true }));
  expect(onClose).toHaveBeenCalledTimes(2);
  // What is shown is 800 by 600, with a scrollbar to its right.
  const body = viewer.querySelector<HTMLElement>(".vw-b")!;
  Object.defineProperty(body, "clientWidth", { value: 800 });
  Object.defineProperty(body, "clientHeight", { value: 600 });
  const press = (x: number, y: number) => {
    const event = new Event("pointerdown", { bubbles: true });
    Object.assign(event, { offsetX: x, offsetY: y });
    fireEvent(body, event);
  };
  press(790, 20);
  expect(onClose).toHaveBeenCalledTimes(3);
  // A press on the scrollbar scrolls.
  press(806, 20);
  expect(onClose).toHaveBeenCalledTimes(3);
  // A press on the file itself is not one beside it.
  fireEvent.pointerDown(
    await waitFor(() => {
      const found = viewer.querySelector(".vw-text");
      expect(found).toBeTruthy();
      return found!;
    }),
  );
  expect(onClose).toHaveBeenCalledTimes(3);
});

it("saves a file that the API has: what it has read of it, if that is all of it", async () => {
  const user = userEvent.setup();
  const save = vi.spyOn(conversation, "saveFile").mockImplementation(() => {});
  const picture = shown("shot.png", "the bytes of a picture");
  const first = show([picture], 0, true);
  await waitFor(() => expect(first.viewer.querySelector("img")).toBeTruthy());
  await user.click(screen.getByRole("button", { name: "Download" }));
  await waitFor(() => expect(save).toHaveBeenCalledTimes(1));
  expect(save.mock.calls[0][0]).toBe("shot.png");
  expect(save.mock.calls[0][1].size).toBe(22);
  expect(picture.read).toHaveBeenCalledTimes(1);
  cleanup();

  // Of a text only the beginning was read.
  const text = shown("errors.log", "0123456789");
  const second = show([text], 0, true);
  await waitFor(() => expect(second.viewer.querySelector("pre")).toBeTruthy());
  await user.click(screen.getByRole("button", { name: "Download" }));
  await waitFor(() => expect(save).toHaveBeenCalledTimes(2));
  expect(vi.mocked(text.read).mock.calls).toEqual([[1 << 20], []]);
  cleanup();

  // A file of the browser's own is not saved.
  show([shown("shot.png", "x")]);
  expect(screen.queryByRole("button", { name: "Download" })).toBeNull();
});
