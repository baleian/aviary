import {
  type Reply,
  type ServerEvent,
  type Transport,
  transport,
} from "./transport.ts";
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

let renew: (() => Promise<void>) | undefined;
let renewing: Promise<void> | undefined;

// renewWith names what renews the token that the requests carry once the API
// has refused it: it authorizes the token that follows, if there is one. A
// request that was refused is then sent again, once.
export function renewWith(how: (() => Promise<void>) | undefined) {
  renew = how;
}

// renewed reports whether the requests carry another token than the one that
// the API refused, after having that one renewed if they do not. Requests
// that are refused at the same time wait for the same renewal.
async function renewed(refused: string): Promise<boolean> {
  if (refused && token === refused && renew) {
    renewing ??= renew()
      .catch(() => {})
      .finally(() => {
        renewing = undefined;
      });
    await renewing;
  }
  return token !== "" && token !== refused;
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

// answered returns the answer to a request. A request that the API refuses
// for its token is asked again, once, with the token that follows. It throws
// an ApiError if the API refuses the request.
async function answered<R extends Reply>(
  ask: (carrier: Transport, token: string) => Promise<R>,
): Promise<R> {
  const carrier = await transport();
  const sent = token;
  let reply = await ask(carrier, sent);
  if (reply.status === 401 && (await renewed(sent))) {
    reply = await ask(carrier, token);
  }
  if (reply.status >= 400) throw refusal(reply);
  return reply;
}

async function send<T>(
  method: string,
  path: string,
  body?: unknown,
): Promise<T> {
  const reply = await answered((carrier, token) =>
    carrier.send(method, path, { token, body }),
  );
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

  // upload sends a file as the body of a POST, and returns the body of the
  // answer. onProgress is called with the bytes of the file that have gone.
  async upload<T>(
    path: string,
    file: Blob,
    onProgress?: (sent: number) => void,
  ): Promise<T> {
    const reply = await answered((carrier, token) =>
      carrier.upload(path, { token, file, onProgress }),
    );
    return reply.body as T;
  },

  // download returns the file that a GET answers with, or no more than its
  // first bytes if it is told how many.
  async download(path: string, first?: number): Promise<Blob> {
    const reply = await answered((carrier, token) =>
      carrier.download(path, { token, first }),
    );
    return reply.file!;
  },

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
    return follow(`/v1/projects/${project}/events`, {
      onOpen: handlers.onOpen,
      onEvent: (event) => {
        if (event.type === "session") {
          handlers.onSession(JSON.parse(event.data) as Session);
        }
      },
      onEnd: () => "now",
      onError: handlers.onError,
    });
  },

  // events follows the events of a turn: GET /v1/turns/{id}/events. It
  // calls onEvent with each event, from the first of the turn to the one
  // that says how the turn ended. A stream that breaks, or that the API
  // ends before that, is opened again and goes on after the last event
  // that came. After a turn.rewound, the events of the turn come again
  // from the first. It gives up, with onError, if the API refuses it. It
  // returns a function that stops following.
  events(
    turn: string,
    handlers: {
      onEvent: (event: TurnEvent) => void;
      onError?: (error: ApiError) => void;
    },
  ): () => void {
    let last = "";
    let over = false;
    return follow(`/v1/turns/${turn}/events`, {
      after: () => last,
      onEvent: (event) => {
        if (event.id) last = event.id;
        over = isLast(event.type);
        handlers.onEvent({ type: event.type, data: JSON.parse(event.data) });
      },
      onEnd: () => (over ? "never" : "later"),
      onError: handlers.onError,
    });
  },
};

// An event of a turn, with its data.
export interface TurnEvent {
  type: string;
  data: Record<string, unknown>;
}

// isLast reports whether an event of the type is the last of its turn: it
// says how the turn ended.
export function isLast(type: string): boolean {
  return [
    "turn.completed",
    "turn.failed",
    "turn.cancelled",
    "turn.expired",
  ].includes(type);
}

// follow keeps a stream of the API open until it is stopped. A stream whose
// connection breaks is opened again, later each time. When the API ends the
// stream, onEnd says when to open it again: now, later as after a break, or
// never. If the API refuses the stream, follow gives up, with onError: but a
// stream that is refused for its token is opened once more if the token can
// be renewed.
function follow(
  path: string,
  handlers: {
    // The ID of the event to go on after, for a stream that can.
    after?: () => string;
    onOpen?: () => void;
    onEvent: (event: ServerEvent) => void;
    onEnd: () => "now" | "later" | "never";
    onError?: (error: ApiError) => void;
  },
): () => void {
  let stopped = false;
  let close = () => {};
  let waiting: ReturnType<typeof setTimeout> | undefined;
  let broken = 0;
  // Whether the token was renewed for the stream since it last opened.
  let retried = false;
  const again = () => {
    const ms = RETRY[Math.min(broken++, RETRY.length - 1)];
    waiting = setTimeout(() => void open(), ms);
  };
  const open = async () => {
    const carrier = await transport();
    if (stopped) return;
    const sent = token;
    close = carrier.stream(
      path,
      { token: sent, after: handlers.after?.() },
      {
        onOpen: () => {
          broken = 0;
          retried = false;
          handlers.onOpen?.();
        },
        onEvent: handlers.onEvent,
        onEnd: () => {
          const next = handlers.onEnd();
          if (next === "now") void open();
          if (next === "later") again();
        },
        onError: (error) => {
          // What is in the way of the API, such as a proxy, answers for
          // it while it restarts.
          if (error instanceof Error || error.status >= 500) return again();
          if (error.status !== 401 || retried) {
            return handlers.onError?.(refusal(error));
          }
          retried = true;
          void renewed(sent).then((newer) => {
            if (stopped) return;
            if (newer) void open();
            else handlers.onError?.(refusal(error));
          });
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
}

// refusal makes the error of an answer that refuses, and tells the listeners
// if it refuses the token.
function refusal(reply: Reply): ApiError {
  if (reply.status === 401) for (const listener of [...refused]) listener();
  return new ApiError(reply.status, reply.body, reply.headers);
}
