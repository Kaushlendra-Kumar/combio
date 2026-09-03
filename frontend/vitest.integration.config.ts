import { defineConfig } from "vitest/config";
import path from "node:path";

/**
 * The integration suite talks to a real Go server over real sockets, so it runs
 * in Node (not jsdom) and gets a longer timeout for the server handshake.
 */
export default defineConfig({
  resolve: { alias: { "@": path.resolve(__dirname, ".") } },
  test: {
    environment: "node",
    include: ["tests/integration/**/*.test.ts"],
    testTimeout: 30_000,
    hookTimeout: 60_000,
    globals: true,
    pool: "forks",
    poolOptions: { forks: { singleFork: true } },
  },
});
