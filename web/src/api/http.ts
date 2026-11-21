import type { Transport } from "./transport.ts";

// http sends the requests to the API that serves the console's own origin.
export const http: Transport = {
  async send(method, path, { token, body }) {
    const headers: Record<string, string> = {};
    if (token) headers["Authorization"] = "Bearer " + token;
    if (body !== undefined) headers["Content-Type"] = "application/json";
    const response = await fetch(path, {
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
};

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
