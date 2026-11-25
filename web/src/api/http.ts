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

  // fetch does not tell how much of a body has gone, so the file goes with
  // an XMLHttpRequest.
  upload(path, { token, file, onProgress }) {
    return new Promise((resolve, reject) => {
      const request = new XMLHttpRequest();
      request.open("POST", address + path);
      if (token) request.setRequestHeader("Authorization", "Bearer " + token);
      request.setRequestHeader(
        "Content-Type",
        file.type || "application/octet-stream",
      );
      request.upload.onprogress = (event) => onProgress?.(event.loaded);
      request.onload = () =>
        resolve({
          status: request.status,
          headers: headersOf(request.getAllResponseHeaders()),
          body: decode(request.responseText),
        });
      request.onerror = () =>
        reject(new TypeError("the file could not be sent"));
      request.onabort = request.onerror;
      request.send(file);
    });
  },

  async download(path, { token }) {
    const headers: Record<string, string> = {};
    if (token) headers["Authorization"] = "Bearer " + token;
    const response = await fetch(address + path, { headers });
    const reply = {
      status: response.status,
      headers: Object.fromEntries(response.headers),
    };
    return response.ok
      ? { ...reply, body: null, file: await response.blob() }
      : { ...reply, body: decode(await response.text()) };
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

// headersOf reads the headers of an answer as an XMLHttpRequest gives them:
// a line each, with the names in lower case.
function headersOf(lines: string): Record<string, string> {
  const headers: Record<string, string> = {};
  for (const line of lines.trim().split(/[\r\n]+/)) {
    const [name, ...value] = line.split(": ");
    if (name) headers[name.toLowerCase()] = value.join(": ");
  }
  return headers;
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
