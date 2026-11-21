import { afterEach, describe, expect, it, vi } from "vitest";
import { http } from "./http.ts";

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
});
