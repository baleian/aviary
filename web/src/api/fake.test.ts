import { beforeAll, expect, it } from "vitest";
import { prefs } from "../lib/prefs.ts";
import { fake } from "./fake.ts";

beforeAll(() => {
  prefs.set("mock.speed", "0");
});

it("returns the headers in lower case", async () => {
  const transport = await fake();
  const reply = await transport.send("GET", "/v1/me", { token: "" });
  expect(reply).toMatchObject({
    status: 401,
    headers: { "www-authenticate": "Bearer" },
  });
});
