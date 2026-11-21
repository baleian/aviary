import { http } from "./http.ts";

// The answer to a request. The names of its headers are in lower case.
export interface Reply {
  status: number;
  headers: Record<string, string>;
  body: unknown;
}

// A Transport carries a request to the API and returns the answer, whatever
// its status. It fails only if no answer comes.
export interface Transport {
  send(
    method: string,
    path: string,
    options: { token: string; body?: unknown },
  ): Promise<Reply>;
}

let chosen: Promise<Transport> | undefined;

// transport returns what carries the requests of the console: HTTP, and in
// development the backend of the mock, which answers in the browser.
export function transport(): Promise<Transport> {
  chosen ??= import.meta.env.DEV
    ? import("./fake.ts").then((module) => module.fake())
    : Promise.resolve(http);
  return chosen;
}
