import { afterEach, describe, expect, it, vi } from "vitest";
import { http, reach } from "./http.ts";

// answers has the API answer the next request, and returns what records
// the request.
function answers(status: number, body = "", headers = {}) {
  const fetch = vi.fn(
    async () => new Response(body || null, { status, headers }),
  );
  vi.stubGlobal("fetch", fetch);
  return fetch;
}

afterEach(() => {
  vi.unstubAllGlobals();
  reach("");
});

describe("http", () => {
  it("sends the token, and the body as JSON", async () => {
    const fetch = answers(201, '{"id": "1"}');
    const reply = await http.send("POST", "/v1/projects", {
      token: "t0",
      body: { slug: "a" },
    });
    expect(fetch).toHaveBeenCalledWith("/v1/projects", {
      method: "POST",
      headers: {
        Authorization: "Bearer t0",
        "Content-Type": "application/json",
      },
      body: '{"slug":"a"}',
    });
    expect(reply).toMatchObject({ status: 201, body: { id: "1" } });
  });

  it("sends neither without a token and a body", async () => {
    const fetch = answers(200, "{}");
    await http.send("GET", "/v1/me", { token: "" });
    expect(fetch).toHaveBeenCalledWith("/v1/me", {
      method: "GET",
      headers: {},
      body: undefined,
    });
  });

  it("returns the headers in lower case", async () => {
    answers(429, '{"code": "queue_full"}', { "Retry-After": "5" });
    const reply = await http.send("POST", "/v1/sessions/1/messages", {
      token: "",
      body: { text: "hello" },
    });
    expect(reply.headers["retry-after"]).toBe("5");
    expect(reply.body).toEqual({ code: "queue_full" });
  });

  it("returns no body for an answer without one", async () => {
    answers(204);
    const reply = await http.send("DELETE", "/v1/members/1", { token: "" });
    expect(reply).toMatchObject({ status: 204, body: null });
  });

  it("returns no body for an answer that is not JSON", async () => {
    answers(502, "<html>Bad Gateway</html>");
    const reply = await http.send("GET", "/v1/me", { token: "" });
    expect(reply).toMatchObject({ status: 502, body: null });
  });

  it("returns the file of an answer, or why there is none", async () => {
    const fetch = answers(200, "to do", { "Content-Type": "text/plain" });
    const reply = await http.download("/v1/attachments/a1", { token: "t0" });
    expect(fetch).toHaveBeenCalledWith("/v1/attachments/a1", {
      headers: { Authorization: "Bearer t0" },
    });
    expect(reply).toMatchObject({ status: 200, body: null });
    expect(reply.file!.type).toBe("text/plain");
    expect(await reply.file!.text()).toBe("to do");

    answers(404, '{"code": "not_found"}');
    const refused = await http.download("/v1/attachments/a2", { token: "" });
    expect(refused).toMatchObject({ status: 404, body: { code: "not_found" } });
    expect(refused.file).toBeUndefined();
  });
});

describe("an API at another address than the console's", () => {
  it("is sent the requests, and asked for the streams", async () => {
    reach("https://api.example.com");
    const fetch = answers(200, "{}");
    await http.send("GET", "/v1/me", { token: "t0" });
    expect(fetch.mock.calls[0]).toEqual([
      "https://api.example.com/v1/me",
      expect.objectContaining({ headers: { Authorization: "Bearer t0" } }),
    ]);

    const { fetch: asked, calls, handlers } = streams(200, []);
    http.stream("/v1/turns/t1/events", { token: "t0" }, handlers);
    await vi.waitFor(() => expect(calls).toContain("end"));
    expect(asked).toHaveBeenCalledWith(
      "https://api.example.com/v1/turns/t1/events",
      expect.anything(),
    );
  });
});

// streams has the API answer the next request with a stream of the chunks,
// and returns what the stream called, in order.
function streams(status: number, chunks: string[]) {
  const body = new ReadableStream<Uint8Array>({
    start(controller) {
      for (const chunk of chunks) {
        controller.enqueue(new TextEncoder().encode(chunk));
      }
      controller.close();
    },
  });
  const fetch = vi.fn(async () => new Response(body, { status }));
  vi.stubGlobal("fetch", fetch);
  const calls: unknown[] = [];
  const handlers = {
    onOpen: () => calls.push("open"),
    onEvent: (event: unknown) => calls.push(event),
    onEnd: () => calls.push("end"),
    onError: (error: unknown) => calls.push({ error }),
  };
  return { fetch, calls, handlers };
}

describe("http.stream", () => {
  it("reads the events of a stream, however they are cut into chunks", async () => {
    const { fetch, calls, handlers } = streams(200, [
      "id: 7-0\nevent: turn.started\ndata: {}\n\nid: 7-1\nevent: mess",
      'age.delta\ndata: {"text": "Hel',
      'lo"}\n\n: a comment keeps the connection alive\n\n',
    ]);
    http.stream("/v1/turns/t1/events", { token: "t0", after: "6-9" }, handlers);
    await vi.waitFor(() => expect(calls).toContain("end"));
    expect(fetch).toHaveBeenCalledWith(
      "/v1/turns/t1/events",
      expect.objectContaining({
        headers: {
          Accept: "text/event-stream",
          Authorization: "Bearer t0",
          "Last-Event-ID": "6-9",
        },
      }),
    );
    expect(calls).toEqual([
      "open",
      { id: "7-0", type: "turn.started", data: "{}" },
      { id: "7-1", type: "message.delta", data: '{"text": "Hello"}' },
      "end",
    ]);
  });

  it("reports that the API refused the stream", async () => {
    const { calls, handlers } = streams(404, ['{"code": "not_found"}']);
    http.stream("/v1/projects/p1/events", { token: "" }, handlers);
    await vi.waitFor(() => expect(calls.length).toBe(1));
    expect(calls[0]).toMatchObject({
      error: { status: 404, body: { code: "not_found" } },
    });
  });

  it("reports that the connection broke, unless the stream was closed", async () => {
    const calls: unknown[] = [];
    const handlers = {
      onOpen: () => calls.push("open"),
      onEvent: () => {},
      onEnd: () => calls.push("end"),
      onError: (error: unknown) => calls.push(error),
    };
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => {
        throw new TypeError("fetch failed");
      }),
    );
    http.stream("/v1/projects/p1/events", { token: "" }, handlers);
    await vi.waitFor(() => expect(calls.length).toBe(1));
    expect(calls[0]).toBeInstanceOf(TypeError);

    const close = http.stream(
      "/v1/projects/p1/events",
      { token: "" },
      handlers,
    );
    close();
    await new Promise((resolve) => setTimeout(resolve, 10));
    expect(calls.length).toBe(1);
  });
});
