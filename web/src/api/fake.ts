import { prefs } from "../lib/prefs.ts";
import type { Reply, Transport } from "./transport.ts";

// An account of the mock's identity provider. Its token is its name.
export interface Account {
  token: string;
  username: string;
  display_name: string;
  // What the account is an example of, such as "Administrator".
  note: string;
}

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
  accounts(): Account[];
  setIdentityProvider(on: boolean): void;
}

// How long an answer takes at the usual pace, in ms: between the two.
const DELAY = [60, 170];

let loaded: Promise<Backend> | undefined;

// backend returns the backend of the mock, which answers the requests of the
// API in the browser, from example data. Two settings of the browser change
// it: "aviary.mock.speed" is its pace, and "aviary.mock.idp" set to "0"
// leaves the installation without an identity provider.
export function backend(): Promise<Backend> {
  loaded ??= load();
  return loaded;
}

async function load(): Promise<Backend> {
  // @ts-expect-error The mock is JavaScript, and has no types.
  const backend: Backend = await import("../../mock/src/js/backend.js");
  backend.mock.speed = Number(prefs.get("mock.speed") ?? 1);
  if (prefs.get("mock.idp") === "0") backend.setIdentityProvider(false);
  return backend;
}

// fake returns the transport of development, which hands the requests to the
// backend of the mock.
export async function fake(): Promise<Transport> {
  const { mock, request } = await backend();
  return {
    async send(method, path, { token, body }) {
      if (mock.speed > 0) {
        const [least, most] = DELAY;
        const ms = least + Math.random() * (most - least);
        await new Promise((resolve) => setTimeout(resolve, ms));
      }
      // The body crosses as JSON, as it would over HTTP.
      const sent = body === undefined ? null : JSON.parse(JSON.stringify(body));
      const reply = request(method, path, { token, body: sent });
      const headers = Object.entries(reply.headers).map(([name, value]) => [
        name.toLowerCase(),
        value,
      ]);
      return { ...reply, headers: Object.fromEntries(headers) };
    },
  };
}
