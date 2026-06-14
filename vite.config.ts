import { vitePlugin as remix } from "@remix-run/dev";
import { defineConfig } from "vitest/config";

export default defineConfig({
  plugins: [remix()],
  test: {
    environment: "node",
    include: ["tests/**/*.test.ts", "tests/**/*.test.tsx"]
  }
});
