import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { prefs } from "../lib/prefs.ts";
import {
  api,
  ApiError,
  authorize,
  isLast,
  onUnauthenticated,
  renewWith,
  type TurnEvent,
} from "./client.ts";
import { backend } from "./fake.ts";
import { transport } from "./transport.ts";
import type { Attachment, Limits, Me, Session, Turn } from "./types.ts";

// The tests run as development does, so the backend of the mock answers. A
// token is the name of one of its accounts, and alice is an administrator.
beforeAll(() => {
  prefs.set("mock.speed", "0");
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

  it("returns the file of an attachment, to a caller that the API knows", async () => {
    const { agent } = await storefront();
    const { sessions } = await api.get<{ sessions: Session[] }>(
      `/v1/agents/${agent}/sessions?limit=100`,
    );
    const triage = sessions.find((each) => each.title.startsWith("Triage"))!;
    const { turns } = await api.get<{ turns: Turn[] }>(
      `/v1/sessions/${triage.id}/turns`,
    );
    const [log] = turns.at(-1)!.attachments!;
    expect(log).toMatchObject({ name: "checkout-errors.log", size: 1843200 });
    const file = await api.download("/v1/attachments/" + log.id);
    expect([file.size, file.type]).toEqual([1843200, "text/plain"]);
    // No more than the beginning of it, if that is asked for.
    const begun = await api.download("/v1/attachments/" + log.id, 64);
    expect([begun.size, begun.type]).toEqual([64, "text/plain"]);
    expect(await begun.text()).toBe((await file.text()).slice(0, 64));

    // The token is renewed for a download as for any request.
    const renew = vi.fn(async () => authorize("alice"));
    renewWith(renew);
    authorize("ghost");
    expect((await api.download("/v1/attachments/" + log.id)).size).toBe(
      1843200,
    );
    expect(renew).toHaveBeenCalledTimes(1);
    renewWith(undefined);

    const none = "/v1/attachments/00000000-0000-7000-8000-000000000000";
    await expect(api.download(none)).rejects.toMatchObject({
      status: 404,
      code: "not_found",
    });
  });

  it("uploads the files of a message, which the message then carries in its order", async () => {
    const { agent } = await storefront();
    const { id } = await api.post<Session>(`/v1/agents/${agent}/sessions`, {
      title: "With files",
    });
    const upload = (name: string, text: string, type = "text/plain") =>
      api.upload<Attachment>(
        `/v1/sessions/${id}/attachments?name=${encodeURIComponent(name)}`,
        new File([text], name, { type }),
      );
    const gone: number[] = [];
    const notes = await api.upload<Attachment>(
      `/v1/sessions/${id}/attachments?name=notes.txt`,
      new File(["to do"], "ignored", { type: "text/plain;charset=utf-8" }),
      (sent) => gone.push(sent),
    );
    expect(notes).toMatchObject({
      name: "notes.txt",
      media_type: "text/plain",
      size: 5,
    });
    expect(gone).toEqual([5]);
    // A name is kept without its directories and leading dots.
    const again = await upload("../.notes.txt", "done", "");
    expect(again).toMatchObject({
      name: "notes.txt",
      media_type: "application/octet-stream",
    });
    const report = await upload("보고서 2026.pdf", "%PDF", "application/pdf");

    const turn = await api.post<Turn>(`/v1/sessions/${id}/messages`, {
      attachments: [report.id, notes.id, again.id],
    });
    // Two files of one name have different names in their message.
    expect(turn.attachments!.map((each) => [each.id, each.name])).toEqual([
      [report.id, "보고서 2026.pdf"],
      [notes.id, "notes.txt"],
      [again.id, "notes (2).txt"],
    ]);
    expect(turn.text).toBe("");
    const record = await api.get<Turn>("/v1/turns/" + turn.id);
    expect(record.attachments).toEqual(turn.attachments);
    // A file is what was uploaded.
    const file = await api.download("/v1/attachments/" + again.id);
    expect(await file.text()).toBe("done");
  });

  it("refuses the files that a message may not carry", async () => {
    const { agent } = await storefront();
    const session = () =>
      api.post<Session>(`/v1/agents/${agent}/sessions`, { title: "Limits" });
    const { id } = await session();
    const files = `/v1/sessions/${id}/attachments`;
    const upload = (path: string, text = "a") =>
      api.upload<Attachment>(path, new File([text], "a.txt"));
    const send = (attachments: unknown, text = "Look") =>
      api.post<Turn>(`/v1/sessions/${id}/messages`, { text, attachments });
    const mock = await backend();
    expect(await api.get<Limits>("/v1/limits")).toEqual({
      max_attachment_bytes: 64 * 1024 * 1024,
      max_attachments: 10,
    });

    await expect(upload(files + "?name=..")).rejects.toMatchObject({
      status: 400,
      code: "invalid_request",
    });
    mock.setLimits(4, 2);
    expect(await api.get<Limits>("/v1/limits")).toEqual({
      max_attachment_bytes: 4,
      max_attachments: 2,
    });
    await expect(upload(files + "?name=a.txt", "12345")).rejects.toMatchObject({
      status: 413,
      code: "attachment_too_large",
    });
    const [first, second, third] = await Promise.all(
      ["a", "b", "c"].map((name) => upload(`${files}?name=${name}.txt`)),
    );
    await expect(send([first.id, second.id, third.id])).rejects.toMatchObject({
      status: 400,
      code: "too_many_attachments",
    });
    // A message carries files of its session that no message carries yet.
    const other = await session();
    const theirs = await upload(`/v1/sessions/${other.id}/attachments?name=a`);
    for (const unknown of [
      [theirs.id],
      [first.id, first.id],
      ["00000000-0000-7000-8000-000000000000"],
    ]) {
      await expect(send(unknown)).rejects.toMatchObject({
        status: 400,
        code: "unknown_attachment",
      });
    }
    await expect(send(undefined, "")).rejects.toMatchObject({
      status: 400,
      code: "invalid_request",
    });
    await send([first.id]);
    await vi.waitFor(async () =>
      expect(
        (await api.get<Session>("/v1/sessions/" + id)).last_turn!.ended_at,
      ).toBeTruthy(),
    );
    await expect(send([first.id])).rejects.toMatchObject({
      code: "unknown_attachment",
    });

    // An installation that takes no files says so.
    mock.setLimits(4, 0);
    expect(await api.get<Limits>("/v1/limits")).toEqual({
      max_attachment_bytes: 0,
      max_attachments: 0,
    });
    await expect(upload(files + "?name=a.txt")).rejects.toMatchObject({
      status: 403,
      code: "attachments_disabled",
    });
    mock.reset();
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

// The API refuses a token that it does not know, as it does one that has
// expired: "ghost" is no account of the example data.
describe("a token that the API refuses", () => {
  afterEach(() => {
    renewWith(undefined);
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  // sends returns the tokens of the requests that are sent from now on.
  async function sends() {
    const send = vi.spyOn(await transport(), "send");
    return () => send.mock.calls.map(([, , options]) => options.token);
  }

  it("is renewed, and the request is sent again", async () => {
    const renew = vi.fn(async () => authorize("alice"));
    renewWith(renew);
    authorize("ghost");
    const me = await api.get<Me>("/v1/me");
    expect(me.username).toBe("alice@example.com");
    expect(renew).toHaveBeenCalledTimes(1);
  });

  it("is renewed once for the requests that it fails together", async () => {
    let finish = () => {};
    const renew = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          finish = () => {
            authorize("alice");
            resolve();
          };
        }),
    );
    renewWith(renew);
    authorize("ghost");
    const both = Promise.all([
      api.get<Me>("/v1/me"),
      api.get<{ projects: unknown[] }>("/v1/projects"),
    ]);
    await vi.waitFor(() => expect(renew).toHaveBeenCalled());
    // The other request is refused while the token is renewed.
    await new Promise((resolve) => setTimeout(resolve, 30));
    finish();
    const [me, read] = await both;
    expect(me.username).toBe("alice@example.com");
    expect(read.projects.length).toBeGreaterThan(0);
    expect(renew).toHaveBeenCalledTimes(1);
  });

  it("is not renewed again for a request that it failed before it was renewed", async () => {
    const carrier = await transport();
    const send = carrier.send.bind(carrier);
    // The answer to the projects comes after the token was renewed.
    let late = () => {};
    vi.spyOn(carrier, "send").mockImplementation(async (...sent) => {
      const reply = await send(...sent);
      if (sent[1] === "/v1/projects" && sent[2].token === "ghost") {
        await new Promise<void>((resolve) => {
          late = resolve;
        });
      }
      return reply;
    });
    const renew = vi.fn(async () => authorize("alice"));
    renewWith(renew);
    authorize("ghost");
    const projects = api.get<{ projects: unknown[] }>("/v1/projects");
    expect((await api.get<Me>("/v1/me")).username).toBe("alice@example.com");
    late();
    expect((await projects).projects.length).toBeGreaterThan(0);
    expect(renew).toHaveBeenCalledTimes(1);
  });

  it("fails the request if it cannot be renewed", async () => {
    const listener = vi.fn();
    const remove = onUnauthenticated(listener);
    const renew = vi.fn(async () => {});
    renewWith(renew);
    authorize("ghost");
    const sent = await sends();
    await expect(api.get("/v1/me")).rejects.toMatchObject({ status: 401 });
    expect(renew).toHaveBeenCalledTimes(1);
    expect(listener).toHaveBeenCalledTimes(1);
    // The request is not sent again with the token that was refused.
    expect(sent()).toEqual(["ghost"]);
    remove();
  });

  it("fails the request if the caller is signed out instead", async () => {
    const renew = vi.fn(async () => authorize(""));
    renewWith(renew);
    authorize("ghost");
    const sent = await sends();
    await expect(api.get("/v1/me")).rejects.toMatchObject({ status: 401 });
    expect(sent()).toEqual(["ghost"]);
  });

  it("fails the request if the token that follows is refused too", async () => {
    const listener = vi.fn();
    const remove = onUnauthenticated(listener);
    const renew = vi.fn(async () => authorize("another ghost"));
    renewWith(renew);
    authorize("ghost");
    const sent = await sends();
    await expect(api.get("/v1/me")).rejects.toMatchObject({ status: 401 });
    expect(renew).toHaveBeenCalledTimes(1);
    expect(listener).toHaveBeenCalledTimes(1);
    expect(sent()).toEqual(["ghost", "another ghost"]);
    remove();
  });

  it("is not renewed for a request that carried none", async () => {
    const renew = vi.fn(async () => authorize("alice"));
    renewWith(renew);
    authorize("");
    await expect(api.get("/v1/me")).rejects.toMatchObject({ status: 401 });
    expect(renew).not.toHaveBeenCalled();
  });

  it("is renewed for a stream, which is opened again", async () => {
    const { project } = await storefront();
    const renew = vi.fn(async () => authorize("alice"));
    renewWith(renew);
    authorize("ghost");
    const onOpen = vi.fn();
    const onError = vi.fn();
    const stop = api.changes(project, { onSession: () => {}, onOpen, onError });
    await vi.waitFor(() => expect(onOpen).toHaveBeenCalledTimes(1));
    expect(renew).toHaveBeenCalledTimes(1);
    expect(onError).not.toHaveBeenCalled();
    stop();
  });

  it("is renewed once for a stream that stays refused", async () => {
    const { project } = await storefront();
    const listener = vi.fn();
    const remove = onUnauthenticated(listener);
    let ghosts = 0;
    const renew = vi.fn(async () => authorize(`ghost ${++ghosts}`));
    renewWith(renew);
    authorize("ghost");
    const onOpen = vi.fn();
    const onError = vi.fn();
    api.changes(project, { onSession: () => {}, onOpen, onError });
    await vi.waitFor(() => expect(onError).toHaveBeenCalledTimes(1));
    expect(onError.mock.calls[0][0]).toMatchObject({ status: 401 });
    expect(renew).toHaveBeenCalledTimes(1);
    expect(listener).toHaveBeenCalledTimes(1);
    expect(onOpen).not.toHaveBeenCalled();
    remove();
  });

  it("is renewed again for a stream that had opened since", async () => {
    const { project } = await storefront();
    const renew = vi.fn(async () => authorize("alice"));
    renewWith(renew);
    authorize("ghost");
    vi.useFakeTimers();
    const onOpen = vi.fn();
    const onError = vi.fn();
    const stop = api.changes(project, { onSession: () => {}, onOpen, onError });
    await vi.waitFor(() => expect(onOpen).toHaveBeenCalledTimes(1));
    // The token expires while the stream is open. The API ends a stream
    // after a few minutes, and refuses the next one.
    authorize("ghost");
    await vi.advanceTimersByTimeAsync(7 * 60 * 1000);
    await vi.waitFor(() => expect(onOpen).toHaveBeenCalledTimes(2));
    expect(renew).toHaveBeenCalledTimes(2);
    expect(onError).not.toHaveBeenCalled();
    stop();
  });

  it("opens a stream once if nothing renews its token", async () => {
    await backend();
    const stream = vi.spyOn(await transport(), "stream");
    authorize("ghost");
    const onError = vi.fn();
    api.changes("not-a-project", { onSession: () => {}, onError });
    await vi.waitFor(() => expect(onError).toHaveBeenCalledTimes(1));
    await new Promise((resolve) => setTimeout(resolve, 40));
    expect(stream).toHaveBeenCalledTimes(1);
  });

  it("leaves a stream alone that was stopped while it was renewed", async () => {
    const { project } = await storefront();
    const listener = vi.fn();
    const remove = onUnauthenticated(listener);
    // Once the token is renewed, and once it cannot be.
    for (const next of ["alice", "ghost"]) {
      let finish = () => {};
      const renew = vi.fn(
        () =>
          new Promise<void>((resolve) => {
            finish = () => {
              authorize(next);
              resolve();
            };
          }),
      );
      renewWith(renew);
      authorize("ghost");
      const onOpen = vi.fn();
      const onError = vi.fn();
      const stop = api.changes(project, {
        onSession: () => {},
        onOpen,
        onError,
      });
      await vi.waitFor(() => expect(renew).toHaveBeenCalled());
      stop();
      finish();
      await new Promise((resolve) => setTimeout(resolve, 60));
      expect(onOpen).not.toHaveBeenCalled();
      expect(onError).not.toHaveBeenCalled();
    }
    expect(listener).not.toHaveBeenCalled();
    remove();
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

  it("opens the stream again at once when the API ends it", async () => {
    const { project } = await storefront();
    const carrier = await transport();
    const stream = vi.spyOn(carrier, "stream");
    stream.mockImplementationOnce((_path, _options, handlers) => {
      queueMicrotask(() => {
        handlers.onOpen();
        handlers.onEnd();
      });
      return () => {};
    });
    vi.useFakeTimers();
    const onOpen = vi.fn();
    const stop = api.changes(project, { onSession: () => {}, onOpen });
    // Far sooner than after a connection that broke.
    await vi.advanceTimersByTimeAsync(20);
    expect(stream).toHaveBeenCalledTimes(2);
    expect(onOpen).toHaveBeenCalledTimes(2);
    stop();
    stream.mockRestore();
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

describe("api.events", () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("knows the events that say how a turn ended", () => {
    const last = ["completed", "failed", "cancelled", "expired"];
    for (const how of last) expect(isLast("turn." + how)).toBe(true);
    for (const type of ["turn.queued", "turn.started", "message.delta"]) {
      expect(isLast(type)).toBe(false);
    }
  });

  // asked sends a message to an agent of the example data that works
  // without asking, and returns its turn once the turn has ended.
  async function asked(text: string) {
    authorize("alice");
    const { projects } = await api.get<{
      projects: { id: string; slug: string }[];
    }>("/v1/projects");
    const project = projects.find((each) => each.slug === "storefront")!;
    const { agents } = await api.get<{
      agents: { id: string; slug: string }[];
    }>(`/v1/projects/${project.id}/agents`);
    const agent = agents.find((each) => each.slug === "reviewer")!;
    const session = await api.post<Session>(`/v1/agents/${agent.id}/sessions`, {
      title: "Followed",
    });
    const turn = await api.post<Turn>(`/v1/sessions/${session.id}/messages`, {
      text,
    });
    await vi.waitFor(async () => {
      const read = await api.get<Turn>("/v1/turns/" + turn.id);
      expect(read.ended_at).toBeTruthy();
    });
    return turn;
  }

  it("tells the events of a turn, from its first to its last", async () => {
    const turn = await asked("What does it do?");
    const carrier = await transport();
    const stream = vi.spyOn(carrier, "stream");
    const events: TurnEvent[] = [];
    api.events(turn.id, { onEvent: (event) => events.push(event) });
    await vi.waitFor(() => expect(events.at(-1)?.type).toBe("turn.completed"));
    expect(events[0].type).toBe("turn.started");
    const said = events.filter((event) => event.type === "message.delta");
    expect(typeof said[0].data.text).toBe("string");
    expect(events.at(-1)!.data.stop_reason).toBe("end_turn");
    // The stream of a turn that has ended is not opened again.
    await new Promise((resolve) => setTimeout(resolve, 80));
    expect(stream).toHaveBeenCalledTimes(1);
  });

  it("goes on after the last event when the stream breaks or ends early", async () => {
    const carrier = await transport();
    const stream = vi.spyOn(carrier, "stream");
    const event = (id: string, type: string) => ({ id, type, data: "{}" });
    // The connection breaks after two events.
    stream.mockImplementationOnce((_path, _options, handlers) => {
      queueMicrotask(() => {
        handlers.onOpen();
        handlers.onEvent(event("7-0", "turn.started"));
        handlers.onEvent(event("7-1", "message.delta"));
        handlers.onError(new TypeError("network error"));
      });
      return () => {};
    });
    // Then the API ends the stream before the turn has ended, as an API
    // that shuts down does.
    stream.mockImplementationOnce((_path, _options, handlers) => {
      queueMicrotask(() => {
        handlers.onOpen();
        handlers.onEvent(event("7-2", "message.delta"));
        // An event without an ID leaves the place in the stream as it is.
        handlers.onEvent(event("", "session.update"));
        handlers.onEnd();
      });
      return () => {};
    });
    stream.mockImplementationOnce((_path, _options, handlers) => {
      queueMicrotask(() => {
        handlers.onOpen();
        // An event that the API makes from the record has no ID.
        handlers.onEvent(event("", "turn.cancelled"));
        handlers.onEnd();
      });
      return () => {};
    });
    vi.useFakeTimers();
    const types: string[] = [];
    const onError = vi.fn();
    api.events("t1", { onEvent: (each) => types.push(each.type), onError });
    await vi.advanceTimersByTimeAsync(900);
    expect(stream).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(200);
    expect(stream).toHaveBeenCalledTimes(2);
    expect(stream.mock.calls[0][1].after).toBe("");
    expect(stream.mock.calls[1][1].after).toBe("7-1");
    await vi.advanceTimersByTimeAsync(1100);
    expect(stream).toHaveBeenCalledTimes(3);
    expect(stream.mock.calls[2][1].after).toBe("7-2");
    expect(stream.mock.calls[2][0]).toBe("/v1/turns/t1/events");
    await vi.advanceTimersByTimeAsync(60 * 1000);
    expect(stream).toHaveBeenCalledTimes(3);
    expect(types).toEqual([
      "turn.started",
      "message.delta",
      "message.delta",
      "session.update",
      "turn.cancelled",
    ]);
    expect(onError).not.toHaveBeenCalled();
  });

  it("tells nothing more once it is stopped", async () => {
    const carrier = await transport();
    const closed = vi.fn();
    let tell!: () => void;
    vi.spyOn(carrier, "stream").mockImplementationOnce(
      (_path, _options, handlers) => {
        tell = () => handlers.onError(new TypeError("network error"));
        return closed;
      },
    );
    vi.useFakeTimers();
    const stop = api.events("t1", { onEvent: () => {} });
    await vi.advanceTimersByTimeAsync(10);
    stop();
    expect(closed).toHaveBeenCalledTimes(1);
    // A stream that breaks after that is not opened again.
    tell();
    stop();
    await vi.advanceTimersByTimeAsync(60 * 1000);
    expect(carrier.stream).toHaveBeenCalledTimes(1);
  });

  it("gives up when the API refuses the stream", async () => {
    authorize("alice");
    const onError = vi.fn();
    api.events("019b0000-0000-7000-8000-000000000000", {
      onEvent: () => {},
      onError,
    });
    await vi.waitFor(() => expect(onError).toHaveBeenCalledTimes(1));
    expect(onError.mock.calls[0][0]).toMatchObject({ status: 404 });
  });
});
