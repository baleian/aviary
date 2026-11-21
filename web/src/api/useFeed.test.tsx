// @vitest-environment happy-dom
import { act, cleanup, renderHook } from "@testing-library/react";
import { afterEach, beforeAll, expect, it, vi } from "vitest";
import { prefs } from "../lib/prefs.ts";
import { queryClient } from "../query.ts";
import { api, authorize } from "./client.ts";
import { useFeed, useFeedReady } from "./feed.ts";
import { transport } from "./transport.ts";

let project = "";

beforeAll(async () => {
  prefs.set("mock.speed", "0");
  authorize("alice");
  const { projects } = await api.get<{ projects: { id: string }[] }>(
    "/v1/projects",
  );
  project = projects[0].id;
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.restoreAllMocks();
});

// following shows the feed of a project, and whether it is ready.
function following(id: string | undefined) {
  return renderHook(() => {
    useFeed(id);
    return useFeedReady();
  });
}

it("is ready once the stream of the project is open, and not before", async () => {
  const carrier = await transport();
  let open = () => {};
  vi.spyOn(carrier, "stream").mockImplementationOnce(
    (_path, _options, handlers) => {
      open = handlers.onOpen;
      return () => {};
    },
  );
  const feed = following(project);
  await new Promise((resolve) => setTimeout(resolve, 20));
  expect(feed.result.current).toBe(false);
  act(() => open());
  expect(feed.result.current).toBe(true);
  // Without a project there is nothing to follow.
  feed.unmount();
  expect(following(undefined).result.current).toBe(false);
});

it("is ready when the stream cannot be opened, so that the console still reads", async () => {
  const feed = following("not-a-project");
  await vi.waitFor(() => expect(feed.result.current).toBe(true));
});

it("has the sessions read again each time the stream has opened again", async () => {
  vi.useFakeTimers();
  const invalidate = vi.spyOn(queryClient, "invalidateQueries");
  const feed = following(project);
  await vi.waitFor(() => expect(feed.result.current).toBe(true));
  // What was read after the first opening has missed nothing.
  expect(invalidate).not.toHaveBeenCalled();
  // The API ends a stream after a few minutes, and the console opens it
  // again.
  await act(() => vi.advanceTimersByTimeAsync(7 * 60 * 1000));
  expect(invalidate).toHaveBeenCalledTimes(1);
  expect(invalidate).toHaveBeenCalledWith({ queryKey: ["sessions"] });
});
