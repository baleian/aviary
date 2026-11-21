import { type Reply, transport } from "./transport.ts";
import type { Session } from "./types.ts";

// ApiError is a request that the API refused.
export class ApiError extends Error {
  readonly status: number;
  // The code of the refusal, such as "not_found", or "" if the answer had
  // none.
  readonly code: string;
  // The seconds after which the API asks to try again, or 0.
  readonly retryAfter: number;

  constructor(status: number, body: unknown, headers: Record<string, string>) {
    const refusal = (body ?? {}) as { code?: unknown; message?: unknown };
    const message =
      typeof refusal.message === "string" && refusal.message
        ? refusal.message
        : `the request failed with status ${status}`;
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = typeof refusal.code === "string" ? refusal.code : "";
    this.retryAfter = Number(headers["retry-after"]) || 0;
  }
}

let token = "";

// authorize sets the token that the requests carry: that of the signed-in
// caller, or "" for none.
export function authorize(value: string) {
  token = value;
}

const refused = new Set<() => void>();

// onUnauthenticated calls the listener whenever the API refuses a request
// for want of a valid token: none was sent, or it has expired. It returns a
// function that removes the listener.
export function onUnauthenticated(listener: () => void): () => void {
  refused.add(listener);
  return () => {
    refused.delete(listener);
  };
}

async function send<T>(
  method: string,
  path: string,
  body?: unknown,
): Promise<T> {
  const carrier = await transport();
  const reply = await carrier.send(method, path, { token, body });
  if (reply.status >= 400) throw refusal(reply);
  return reply.body as T;
}

// How long to wait before a stream that broke is opened again, in ms: longer
// each time, up to the last.
const RETRY = [1000, 2000, 5000, 15000];

// api sends a request of the API and returns the body of its answer. It
// throws an ApiError if the API refuses the request.
export const api = {
  get: <T>(path: string) => send<T>("GET", path),
  post: <T>(path: string, body?: unknown) => send<T>("POST", path, body),
  put: <T>(path: string, body: unknown) => send<T>("PUT", path, body),
  patch: <T>(path: string, body: unknown) => send<T>("PATCH", path, body),
  delete: <T>(path: string) => send<T>("DELETE", path),

  // changes follows what changes in a project: GET /v1/projects/{id}/events.
  // It calls onSession with every session of the project that changes and
  // that the caller may read, as the session is after the change. The
  // stream does not repeat what happened before it opened, so onOpen is
  // called each time it has opened: what was read before that may be out of
  // date. It opens again when the API ends it, as the API does every few
  // minutes, and when the connection breaks. It gives up, with onError, if
  // the API refuses it. It returns a function that stops following.
  changes(
    project: string,
    handlers: {
      onSession: (session: Session) => void;
      onOpen?: () => void;
      onError?: (error: ApiError) => void;
    },
  ): () => void {
    let stopped = false;
    let close = () => {};
    let waiting: ReturnType<typeof setTimeout> | undefined;
    let broken = 0;
    const again = () => {
      const ms = RETRY[Math.min(broken++, RETRY.length - 1)];
      waiting = setTimeout(() => void open(), ms);
    };
    const open = async () => {
      const carrier = await transport();
      if (stopped) return;
      close = carrier.stream(
        `/v1/projects/${project}/events`,
        { token },
        {
          onOpen: () => {
            broken = 0;
            handlers.onOpen?.();
          },
          onEvent: (event) => {
            if (event.type === "session") {
              handlers.onSession(JSON.parse(event.data) as Session);
            }
          },
          onEnd: () => void open(),
          onError: (error) => {
            // What is in the way of the API, such as a proxy, answers for
            // it while it restarts.
            if (error instanceof Error || error.status >= 500) return again();
            handlers.onError?.(refusal(error));
          },
        },
      );
    };
    void open();
    return () => {
      stopped = true;
      clearTimeout(waiting);
      close();
    };
  },
};

// refusal makes the error of an answer that refuses, and tells the listeners
// if it refuses the token.
function refusal(reply: Reply): ApiError {
  if (reply.status === 401) for (const listener of [...refused]) listener();
  return new ApiError(reply.status, reply.body, reply.headers);
}
