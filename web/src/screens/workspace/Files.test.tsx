// @vitest-environment happy-dom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { api } from "../../api/client.ts";
import type { Attachment } from "../../api/types.ts";
import { thumbnail } from "../../lib/thumbnail.ts";
import { Carried } from "./Files.tsx";

// The browser of the tests draws nothing: a picture of a file is its name.
vi.mock("../../lib/thumbnail.ts", () => ({
  thumbnail: vi.fn(async (file: Blob) => "data:," + (await file.text())),
}));

// What watches whether the cards are near, and what it was told to call.
let watching: (() => void)[] = [];
// near has every card that is watched be near what the window shows.
const near = () => watching.splice(0).forEach((tell) => tell());

beforeEach(() => {
  watching = [];
  vi.stubGlobal(
    "IntersectionObserver",
    class {
      tell: IntersectionObserverCallback;
      constructor(tell: IntersectionObserverCallback) {
        this.tell = tell;
      }
      observe() {
        watching.push(() =>
          this.tell(
            [{ isIntersecting: true } as IntersectionObserverEntry],
            this as unknown as IntersectionObserver,
          ),
        );
      }
      disconnect() {}
    },
  );
  vi.spyOn(api, "download").mockImplementation((path) =>
    Promise.resolve(new Blob([path.split("/").pop()!])),
  );
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  vi.mocked(thumbnail).mockClear();
});

const file = (name: string, media_type: string, size = 1024): Attachment => ({
  id: name,
  name,
  media_type,
  size,
});

const FILES = [
  file("notes.txt", "text/plain"),
  file("board.xlsx", "application/octet-stream"),
  file("image.png", "image/png"),
  file("design.pdf", "application/pdf"),
  // Too large to be read for a picture of it.
  file("poster.png", "image/png", (16 << 20) + 1),
];

function show(files: Attachment[], kept = true, client = new QueryClient()) {
  render(
    <QueryClientProvider client={client}>
      <Carried files={files} kept={kept} />
    </QueryClientProvider>,
  );
  const cards = [...document.querySelectorAll<HTMLElement>(".files .file")];
  return { client, cards };
}

// pictures returns the picture of each card, if it has one.
const pictures = () =>
  [...document.querySelectorAll(".files .file")].map(
    (card) => card.querySelector("img")?.getAttribute("src") ?? null,
  );

it("draws a picture of the files that have one, once they are near, and reads no file twice for it", async () => {
  const { client } = show(FILES);
  // Until then a tile says what the file is.
  expect(
    [...document.querySelectorAll(".files .tile")].map((tile) => [
      tile.className,
      tile.textContent,
    ]),
  ).toEqual([
    ["tile text", "txt"],
    ["tile sheet", "xlsx"],
    ["tile image", "png"],
    ["tile pdf", "pdf"],
    ["tile image", "png"],
  ]);
  expect(api.download).not.toHaveBeenCalled();

  near();
  await vi.waitFor(() =>
    expect(pictures()).toEqual([null, null, "data:,image.png", null, null]),
  );
  // Only what can be drawn was read, and not what is too large.
  expect(vi.mocked(api.download).mock.calls).toEqual([
    ["/v1/attachments/image.png"],
  ]);
  expect(vi.mocked(thumbnail).mock.calls[0][1]).toEqual({
    ext: "png",
    family: "image",
    view: "image",
    type: "image/png",
  });
  cleanup();

  // What was drawn once is not read again.
  show(FILES, true, client);
  expect(pictures()[2]).toBe("data:,image.png");
  near();
  expect(api.download).toHaveBeenCalledTimes(1);
});

it("keeps the tile of a file that could not be drawn or read", async () => {
  vi.mocked(thumbnail).mockResolvedValueOnce(null);
  vi.mocked(api.download)
    .mockResolvedValueOnce(new Blob(["not a picture"]))
    .mockRejectedValueOnce(new Error("no such resource"));
  show([file("image.png", "image/png"), file("shot.jpg", "image/jpeg")]);
  near();
  await vi.waitFor(() => expect(api.download).toHaveBeenCalledTimes(2));
  await new Promise((resolve) => setTimeout(resolve, 0));
  expect(pictures()).toEqual([null, null]);
  // Neither is tried again.
  expect(api.download).toHaveBeenCalledTimes(2);
});

it("does not read the files of a session that has ended, which are kept no longer", () => {
  const { cards } = show(FILES, false);
  near();
  expect(api.download).not.toHaveBeenCalled();
  expect(document.querySelector(".files button")).toBeNull();
  expect(cards[2].querySelector(".what")!.textContent).toBe("image.png1 KB");
});
