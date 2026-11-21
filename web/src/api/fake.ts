import { prefs } from "../lib/prefs.ts";
import type { Reply, ServerEvent, Transport } from "./transport.ts";

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
  // pace, and 0 is at once. scenario is what the next turn plays, such as
  // "queue" for a turn that waits in the queue first.
  mock: { speed: number; scenario: string };
  request(
    method: string,
    path: string,
    options: { token: string; body: unknown },
  ): Reply;
  // open answers GET /v1/turns/{id}/events, and watch answers
  // GET /v1/projects/{id}/events. Each returns the answer of the API, and
  // close() if it accepted the stream.
  open(
    turn: string,
    options: { token: string; after: string },
    onEvent: (event: ServerEvent) => void,
    onEnd: () => void,
  ): { response: Reply; close?: () => void };
  watch(
    project: string,
    options: { token: string },
    onEvent: (event: ServerEvent) => void,
    onEnd: () => void,
  ): { response: Reply; close?: () => void };
  accounts(): Account[];
  setIdentityProvider(on: boolean): void;
  // reset puts the example data back as it began.
  reset(): void;
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
  // For the scripts that check the console in a browser: the API itself, to
  // ask it as another caller would.
  if (typeof window !== "undefined")
    Object.assign(window, { aviaryMock: backend });
  return backend;
}

// The streams of the API, and what of the backend answers each.
const STREAMS = /^\/v1\/(projects|turns)\/([^/]+)\/events$/;

// fake returns the transport of development, which hands the requests to the
// backend of the mock.
export async function fake(): Promise<Transport> {
  const { mock, request, open, watch } = await backend();
  return {
    stream(path, { token, after = "" }, handlers) {
      let closed = false;
      // The backend calls back while it still answers, and a stream of the
      // API never does: its events come later.
      const later = (call: () => void) =>
        queueMicrotask(() => {
          if (!closed) call();
        });
      const [, kind, id] = STREAMS.exec(path) ?? [];
      const onEvent = (event: ServerEvent) =>
        later(() => handlers.onEvent({ ...event, id: event.id ?? "" }));
      const onEnd = () => later(handlers.onEnd);
      const stream =
        kind === "turns"
          ? open(id, { token, after }, onEvent, onEnd)
          : watch(id, { token }, onEvent, onEnd);
      if (stream.response.status !== 200) {
        const reply = lowercased(stream.response);
        later(() => handlers.onError(reply));
      } else {
        later(handlers.onOpen);
      }
      return () => {
        closed = true;
        stream.close?.();
      };
    },

    async send(method, path, { token, body }) {
      if (mock.speed > 0) {
        const [least, most] = DELAY;
        const ms = least + Math.random() * (most - least);
        await new Promise((resolve) => setTimeout(resolve, ms));
      }
      // The body crosses as JSON, as it would over HTTP.
      const sent = body === undefined ? null : JSON.parse(JSON.stringify(body));
      return lowercased(request(method, path, { token, body: sent }));
    },
  };
}

// lowercased returns an answer of the backend with the names of its headers
// in lower case, as a transport gives them.
function lowercased(reply: Reply): Reply {
  const headers = Object.entries(reply.headers).map(([name, value]) => [
    name.toLowerCase(),
    value,
  ]);
  return { ...reply, headers: Object.fromEntries(headers) };
}
