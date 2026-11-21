/// <reference types="vitest/config" />
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

export default defineConfig({
  plugins: [react()],
  test: {
    // Node's own localStorage warns unless it is given a file to keep.
    execArgv: ["--no-experimental-webstorage"],
  },
});
