/// <reference types="vitest/config" />
import { readFileSync } from "node:fs";
import { basename, dirname, join } from "node:path";
import react from "@vitejs/plugin-react";
import { defineConfig, loadEnv, type Plugin, type ProxyOptions } from "vite";

// AVIARY_API names an API for the development server to pass the requests
// of the console on to, such as http://localhost:8080. Without it the
// console is answered from example data, in the browser.
//
// AVIARY_PUBLIC_API names an API that the browser calls itself, at another
// address than the console's: one that allows the origin of the console.
//
// If the API authenticates its callers, AVIARY_OIDC_ISSUER and
// AVIARY_OIDC_CLIENT_ID name the OpenID Connect issuer and the client that
// the console signs them in with, and AVIARY_OIDC_SCOPES the scopes that it
// asks for, if not "openid profile email".
export default defineConfig(({ command, mode }) => {
  const env = loadEnv(mode, process.cwd(), "AVIARY_");
  const api = env.AVIARY_API;
  // The tests are answered from the example data, whatever API the
  // environment names. A build never is.
  const fake =
    mode === "test" || (command === "serve" && !api && !env.AVIARY_PUBLIC_API);
  return {
    plugins: [react(), settings(env), licenses()],
    define: { __FAKE_API__: JSON.stringify(fake) },
    server: { proxy: api ? { "/v1": passOn(api) } : undefined },
    test: {
      // Node's own localStorage warns unless it is given a file to keep.
      execArgv: ["--no-experimental-webstorage"],
    },
  };
});

// settings has the development server serve what an installation sets for
// its console, at /config.json, from the environment.
function settings(env: Record<string, string>): Plugin {
  const body = JSON.stringify({
    api: env.AVIARY_PUBLIC_API ?? "",
    oidc: {
      issuer: env.AVIARY_OIDC_ISSUER ?? "",
      clientId: env.AVIARY_OIDC_CLIENT_ID ?? "",
      scopes: env.AVIARY_OIDC_SCOPES ?? "",
    },
  });
  return {
    name: "aviary-settings",
    configureServer(server) {
      server.middlewares.use("/config.json", (_request, response) => {
        response.setHeader("Content-Type", "application/json");
        response.end(body);
      });
    },
  };
}

// The folder of the package that a file comes from.
const PACKAGE = /^.*node_modules\/(@[^/]+\/)?[^/]+/;

// licenses emits the license of each package that the build takes a font
// from, next to the files of the font. Font licenses such as the OFL have to
// accompany every copy.
function licenses(): Plugin {
  return {
    name: "aviary-licenses",
    generateBundle(_options, bundle) {
      const found = new Map<string, string>();
      for (const file of Object.values(bundle)) {
        if (file.type !== "asset" || !file.fileName.endsWith(".woff2"))
          continue;
        for (const source of file.originalFileNames) {
          const [from] = PACKAGE.exec(source) ?? [];
          if (!from) continue;
          found.set(
            join(dirname(file.fileName), `${basename(from)}-LICENSE.txt`),
            join(from, "LICENSE"),
          );
        }
      }
      for (const [fileName, license] of found) {
        this.emitFile({
          type: "asset",
          fileName,
          source: readFileSync(license),
        });
      }
    },
  };
}

// passOn returns how the development server passes requests on to an API.
function passOn(target: string): ProxyOptions {
  return {
    target,
    changeOrigin: true,
    configure(proxy) {
      // A stream of events is open once its headers have come, which may be
      // long before its first event. They are passed on at once, and not
      // with the first of what follows them.
      proxy.on("proxyRes", (answer, _request, response) => {
        if (answer.headers["content-type"]?.startsWith("text/event-stream")) {
          setImmediate(() => response.flushHeaders());
        }
      });
    },
  };
}
