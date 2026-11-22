import { EventSourceParserStream } from "eventsource-parser/stream";
import type { StreamHandlers, Transport } from "./transport.ts";

let address = "";

// reach names the address of the API, if it is not the console's own.
export function reach(api: string) {
  address = api;
}

// http sends the requests to the API: at the console's own address, unless
// another was named.
export const http: Transport = {
  async send(method, path, { token, body }) {
    const headers: Record<string, string> = {};
    if (token) headers["Authorization"] = "Bearer " + token;
    if (body !== undefined) headers["Content-Type"] = "application/json";
    const response = await fetch(address + path, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    return {
      status: response.status,
      headers: Object.fromEntries(response.headers),
      body: decode(await response.text()),
    };
  },

  stream(path, { token, after }, handlers) {
    const closing = new AbortController();
    const headers: Record<string, string> = { Accept: "text/event-stream" };
    if (token) headers["Authorization"] = "Bearer " + token;
    if (after) headers["Last-Event-ID"] = after;
    follow(address + path, headers, closing.signal, handlers).catch((error) => {
      if (!closing.signal.aborted) handlers.onError(error);
    });
    return () => closing.abort();
  },
};

// follow reads a stream of server-sent events to its end. EventSource is not
// used, because it cannot send the Authorization header.
async function follow(
  path: string,
  headers: Record<string, string>,
  signal: AbortSignal,
  handlers: StreamHandlers,
) {
  const response = await fetch(path, { headers, signal });
  if (!response.ok || !response.body) {
    handlers.onError({
      status: response.status,
      headers: Object.fromEntries(response.headers),
      body: decode(await response.text()),
    });
    return;
  }
  handlers.onOpen();
  const events = response.body
    .pipeThrough(new TextDecoderStream())
    .pipeThrough(new EventSourceParserStream());
  for await (const event of events) {
    if (signal.aborted) return;
    handlers.onEvent({
      id: event.id ?? "",
      type: event.event ?? "message",
      data: event.data,
    });
  }
  if (!signal.aborted) handlers.onEnd();
}

// decode reads the body of an answer. What is in the way of the API, such as
// a proxy, may answer with something other than JSON.
function decode(text: string): unknown {
  if (!text) return null;
  try {
    return JSON.parse(text);
  } catch {
    return null;
  }
}
