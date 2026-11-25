import { http } from "./http.ts";

// The answer to a request. The names of its headers are in lower case.
export interface Reply {
  status: number;
  headers: Record<string, string>;
  body: unknown;
}

// An event of a stream of server-sent events. The data is JSON text.
export interface ServerEvent {
  id: string;
  type: string;
  data: string;
}

// What a stream calls as it goes: onOpen once the API has accepted it, then
// onEvent for each event, and at last either onEnd, when the API ends the
// stream, or onError, when the API refuses it or the connection breaks.
export interface StreamHandlers {
  onOpen: () => void;
  onEvent: (event: ServerEvent) => void;
  onEnd: () => void;
  onError: (error: Reply | Error) => void;
}

// A Transport carries the requests of the console to the API.
export interface Transport {
  // send returns the answer to a request, whatever its status. It fails
  // only if no answer comes.
  send(
    method: string,
    path: string,
    options: { token: string; body?: unknown },
  ): Promise<Reply>;

  // upload returns the answer to a POST whose body is the file, of the type
  // that the file says it is. onProgress is called with the bytes of the
  // file that have gone, as they go.
  upload(
    path: string,
    options: {
      token: string;
      file: Blob;
      onProgress?: (sent: number) => void;
    },
  ): Promise<Reply>;

  // download returns the answer to a GET, with the file that it carries if
  // it does not refuse: all of the file, or no more than its first bytes if
  // it is told how many.
  download(
    path: string,
    options: { token: string; first?: number },
  ): Promise<Reply & { file?: Blob }>;

  // stream follows the server-sent events of a path, after the event with
  // the ID if one is given. It returns a function that closes the stream,
  // after which nothing more is called.
  stream(
    path: string,
    options: { token: string; after?: string },
    handlers: StreamHandlers,
  ): () => void;
}

let chosen: Promise<Transport> | undefined;

// transport returns what carries the requests of the console: HTTP, or the
// backend of the mock, which answers from example data in the browser.
export function transport(): Promise<Transport> {
  chosen ??= __FAKE_API__
    ? import("./fake.ts").then((module) => module.fake())
    : Promise.resolve(http);
  return chosen;
}
