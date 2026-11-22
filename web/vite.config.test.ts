import { mkdtemp, rm } from "node:fs/promises";
import { createServer as createApi } from "node:http";
import type { AddressInfo } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { build, createServer, type Rolldown } from "vite";
import { afterEach, expect, it, vi } from "vitest";

const closing: (() => unknown)[] = [];

afterEach(async () => {
  vi.unstubAllEnvs();
  for (const close of closing.splice(0)) await close();
});

// serving starts the development server with an API to pass requests on
// to, and returns where the development server listens. The API is what
// answers a request.
async function serving(api: Parameters<typeof createApi>[1]) {
  const upstream = createApi(api);
  await new Promise<void>((resolve) =>
    upstream.listen(0, "127.0.0.1", resolve),
  );
  closing.push(() => upstream.closeAllConnections());
  closing.push(() => upstream.close());
  const { port } = upstream.address() as AddressInfo;
  vi.stubEnv("AVIARY_API", `http://127.0.0.1:${port}`);
  // The server keeps what it prepares in a folder of its own, and prepares
  // no dependencies: a development server that runs meanwhile keeps its own.
  const cacheDir = await mkdtemp(join(tmpdir(), "aviary-web-"));
  closing.push(() => rm(cacheDir, { recursive: true, force: true }));
  const server = await createServer({
    mode: "development",
    logLevel: "silent",
    cacheDir,
    optimizeDeps: { noDiscovery: true, include: [] },
    server: { port: 0, host: "127.0.0.1" },
  });
  await server.listen();
  closing.push(() => server.close());
  return server.resolvedUrls!.local[0];
}

it("passes the requests of the API on to the API that is named", async () => {
  const url = await serving((request, response) => {
    response.writeHead(200, { "Content-Type": "application/json" });
    response.end(JSON.stringify({ asked: request.url }));
  });
  const answer = await fetch(url + "v1/me");
  expect(await answer.json()).toEqual({ asked: "/v1/me" });
});

it("serves the settings of the console from the environment", async () => {
  vi.stubEnv("AVIARY_OIDC_ISSUER", "https://login.example.com/realms/main");
  vi.stubEnv("AVIARY_OIDC_CLIENT_ID", "aviary");
  const url = await serving((_request, response) => response.end());
  const answer = await fetch(url + "config.json");
  expect(answer.headers.get("content-type")).toBe("application/json");
  expect(await answer.json()).toEqual({
    api: "",
    oidc: {
      issuer: "https://login.example.com/realms/main",
      clientId: "aviary",
      scopes: "",
    },
  });
});

it("tells the console of an API that the browser calls itself", async () => {
  vi.stubEnv("AVIARY_PUBLIC_API", "https://api.example.com");
  const url = await serving((_request, response) => response.end());
  const answer = await fetch(url + "config.json");
  expect(await answer.json()).toMatchObject({ api: "https://api.example.com" });
});

it("passes the headers of an event stream on before its first event", async () => {
  // The stream is open, and nothing happens in it for a long time.
  const url = await serving((_request, response) => {
    response.writeHead(200, { "Content-Type": "text/event-stream" });
    response.flushHeaders();
  });
  const leaving = new AbortController();
  closing.unshift(() => leaving.abort());
  const answer = await Promise.race([
    fetch(url + "v1/projects/p1/events", { signal: leaving.signal }),
    new Promise<null>((resolve) => setTimeout(() => resolve(null), 2000)),
  ]);
  expect(answer?.status).toBe(200);
  expect(answer?.headers.get("content-type")).toBe("text/event-stream");
});

it("builds the licenses of the packages that it bundles in", async () => {
  const { output } = (await build({
    mode: "production",
    logLevel: "silent",
    build: { write: false },
  })) as Rolldown.RolldownOutput;
  const file = output.find((file) => file.fileName === "licenses.md");
  const licenses = file?.type === "asset" ? file.source.toString() : "";
  expect(licenses).toMatch(/^## react - \S+ \(MIT\)$/m);
  // The fonts too, which only the styles name.
  const fonts = output.filter(
    (file) => file.type === "asset" && file.fileName.endsWith(".woff2"),
  ) as Rolldown.OutputAsset[];
  expect(fonts.length).toBeGreaterThan(0);
  for (const font of fonts) {
    const [, from] =
      /node_modules\/((?:@[^/]+\/)?[^/]+)\//.exec(font.originalFileNames[0]) ??
      [];
    expect(licenses, font.fileName).toContain(`## ${from} - `);
  }
  expect(licenses).toContain("SIL OPEN FONT LICENSE");
});
