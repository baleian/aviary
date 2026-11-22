/// <reference types="vitest/config" />
import react from "@vitejs/plugin-react";
import { defineConfig, loadEnv, type ProxyOptions } from "vite";

// AVIARY_API names an API for the development server to pass the requests
// of the console on to, such as http://localhost:8080. Without it the
// console is answered from example data, in the browser.
export default defineConfig(({ command, mode }) => {
  const api = loadEnv(mode, process.cwd(), "AVIARY_").AVIARY_API;
  // The tests are answered from the example data, whatever API the
  // environment names. A build never is.
  const fake = mode === "test" || (command === "serve" && !api);
  return {
    plugins: [react()],
    define: { __FAKE_API__: JSON.stringify(fake) },
    server: { proxy: api ? { "/v1": passOn(api) } : undefined },
    test: {
      // Node's own localStorage warns unless it is given a file to keep.
      execArgv: ["--no-experimental-webstorage"],
    },
  };
});

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
