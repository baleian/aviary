import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { prefs } from "../lib/prefs.ts";
import { api, ApiError, authorize, onUnauthenticated } from "./client.ts";
import { backend } from "./fake.ts";
import { transport } from "./transport.ts";
import type { Me, Session } from "./types.ts";

// The tests run as development does, so the backend of the mock answers. A
// token is the name of one of its accounts, and alice is an administrator.
beforeAll(() => {
  prefs.set("mock.speed", "0");
});

describe("api", () => {
  it("returns the body of the answer", async () => {
    authorize("alice");
    const me = await api.get<Me>("/v1/me");
    expect(me).toMatchObject({ username: "alice@example.com", admin: true });
  });

  it("sends the body of the request", async () => {
    authorize("alice");
    const project = await api.post("/v1/projects", {
      slug: "from-a-test",
      name: "From a test",
    });
    expect(project).toMatchObject({ slug: "from-a-test", name: "From a test" });
  });

  it("throws what the API refuses", async () => {
    authorize("alice");
    const refusal = await api.get("/v1/projects/none").catch((error) => error);
    expect(refusal).toBeInstanceOf(ApiError);
    expect(refusal).toMatchObject({
      status: 404,
      code: "not_found",
      message: "no such resource",
    });
  });

  it("tells its listeners that the token was refused", async () => {
    const listener = vi.fn();
    const remove = onUnauthenticated(listener);
    authorize("");
    await expect(api.get("/v1/me")).rejects.toMatchObject({
      status: 401,
      code: "unauthenticated",
    });
    expect(listener).toHaveBeenCalledTimes(1);

    remove();
    await expect(api.get("/v1/me")).rejects.toMatchObject({ status: 401 });
    expect(listener).toHaveBeenCalledTimes(1);
  });
});

describe("ApiError", () => {
  it("reads when to try again", () => {
    const body = { code: "queue_full", message: "too many turns are queued" };
    const error = new ApiError(429, body, { "retry-after": "7" });
    expect(error).toMatchObject({ code: "queue_full", retryAfter: 7 });
  });

  it("names the status of an answer without a body", () => {
    const error = new ApiError(502, null, {});
    expect(error).toMatchObject({
      message: "the request failed with status 502",
      code: "",
      retryAfter: 0,
    });
  });
});

describe("api.changes", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  // storefront returns the project of the example data, and one of its
  // agents, as Alice reads them.
  async function storefront() {
    authorize("alice");
    const { projects } = await api.get<{
      projects: { id: string; slug: string }[];
    }>("/v1/projects");
    const project = projects.find((each) => each.slug === "storefront")!;
    const { agents } = await api.get<{ agents: { id: string }[] }>(
      `/v1/projects/${project.id}/agents`,
    );
    return { project: project.id, agent: agents[0].id };
  }

  it("tells of a session of the project when it changes", async () => {
    const { project, agent } = await storefront();
    const sessions: Session[] = [];
    const onOpen = vi.fn();
    const stop = api.changes(project, {
      onSession: (session) => sessions.push(session),
      onOpen,
    });
    await vi.waitFor(() => expect(onOpen).toHaveBeenCalledTimes(1));
    const created = await api.post<Session>(`/v1/agents/${agent}/sessions`, {
      title: "Told by the stream",
    });
    await vi.waitFor(() => expect(sessions.length).toBe(1));
    expect(sessions[0]).toMatchObject({ id: created.id, agent_id: agent });

    // After it is stopped, nothing more is told.
    stop();
    await api.patch(`/v1/sessions/${created.id}`, { title: "Renamed" });
    await new Promise((resolve) => setTimeout(resolve, 80));
    expect(sessions.length).toBe(1);
  });

  it("opens the stream again when the API ends it", async () => {
    const { project } = await storefront();
    vi.useFakeTimers();
    const onOpen = vi.fn();
    const stop = api.changes(project, { onSession: () => {}, onOpen });
    await vi.waitFor(() => expect(onOpen).toHaveBeenCalledTimes(1));
    // The API ends a stream after a few minutes.
    await vi.advanceTimersByTimeAsync(7 * 60 * 1000);
    expect(onOpen).toHaveBeenCalledTimes(2);
    stop();
    await vi.advanceTimersByTimeAsync(7 * 60 * 1000);
    expect(onOpen).toHaveBeenCalledTimes(2);
  });

  it("opens the stream again, later, when the connection breaks", async () => {
    const { project } = await storefront();
    const carrier = await transport();
    const stream = vi.spyOn(carrier, "stream");
    // The first two tries fail: no connection, and then something in the
    // way of the API that answers for it.
    stream.mockImplementationOnce((_path, _options, handlers) => {
      queueMicrotask(() => handlers.onError(new TypeError("fetch failed")));
      return () => {};
    });
    stream.mockImplementationOnce((_path, _options, handlers) => {
      queueMicrotask(() =>
        handlers.onError({ status: 502, headers: {}, body: null }),
      );
      return () => {};
    });
    vi.useFakeTimers();
    const onOpen = vi.fn();
    const onError = vi.fn();
    const stop = api.changes(project, { onSession: () => {}, onOpen, onError });
    await vi.advanceTimersByTimeAsync(900);
    expect(stream).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(200);
    expect(stream).toHaveBeenCalledTimes(2);
    // The second wait is longer than the first.
    await vi.advanceTimersByTimeAsync(1500);
    expect(stream).toHaveBeenCalledTimes(2);
    await vi.advanceTimersByTimeAsync(600);
    expect(stream).toHaveBeenCalledTimes(3);
    expect(onOpen).toHaveBeenCalledTimes(1);
    expect(onError).not.toHaveBeenCalled();
    stop();
    stream.mockRestore();
  });

  it("gives up when the API refuses the stream", async () => {
    await backend();
    const refusedToken = vi.fn();
    const remove = onUnauthenticated(refusedToken);
    authorize("ghost");
    const onError = vi.fn();
    const onOpen = vi.fn();
    api.changes("not-a-project", { onSession: () => {}, onOpen, onError });
    await vi.waitFor(() => expect(onError).toHaveBeenCalledTimes(1));
    expect(onError.mock.calls[0][0]).toMatchObject({ status: 401 });
    expect(refusedToken).toHaveBeenCalledTimes(1);
    expect(onOpen).not.toHaveBeenCalled();
    remove();
  });
});
