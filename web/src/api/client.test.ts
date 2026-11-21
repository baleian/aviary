import { beforeAll, describe, expect, it, vi } from "vitest";
import { prefs } from "../lib/prefs.ts";
import { api, ApiError, authorize, onUnauthenticated } from "./client.ts";
import type { Me } from "./types.ts";

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
