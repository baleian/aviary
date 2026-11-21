import { prefs } from "../lib/prefs.ts";
import type { Reply, Transport } from "./transport.ts";

// What the console uses of web/mock/src/js/backend.js.
interface Backend {
  // What the mock is set to do. speed is how fast it works: 1 is its usual
  // pace, and 0 is at once.
  mock: { speed: number };
  request(
    method: string,
    path: string,
    options: { token: string; body: unknown },
  ): Reply;
}

// How long an answer takes at the usual pace, in ms: between the two.
const DELAY = [60, 170];

// fake returns the transport of development: the backend of the mock, which
// answers the requests of the API in the browser, from example data. A token
// is the name of one of its accounts.
export async function fake(): Promise<Transport> {
  // @ts-expect-error The mock is JavaScript, and has no types.
  const backend: Backend = await import("../../mock/src/js/backend.js");
  backend.mock.speed = Number(prefs.get("mock.speed") ?? 1);
  return {
    async send(method, path, { token, body }) {
      if (backend.mock.speed > 0) {
        const [least, most] = DELAY;
        const ms = least + Math.random() * (most - least);
        await new Promise((resolve) => setTimeout(resolve, ms));
      }
      // The body crosses as JSON, as it would over HTTP.
      const sent = body === undefined ? null : JSON.parse(JSON.stringify(body));
      const reply = backend.request(method, path, { token, body: sent });
      const headers = Object.entries(reply.headers).map(([name, value]) => [
        name.toLowerCase(),
        value,
      ]);
      return { ...reply, headers: Object.fromEntries(headers) };
    },
  };
}
