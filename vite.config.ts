import { vitePlugin as remix } from "@remix-run/dev";
import { defineConfig } from "vite";

const isStorybook = process.env.STORYBOOK === "true";

export default defineConfig({
  plugins: [
    !isStorybook && remix({
      future: {
        v3_fetcherPersist: true,
        v3_relativeSplatPath: true,
        v3_throwAbortReason: true,
        v3_singleFetch: true,
        v3_lazyRouteDiscovery: true,
      },
    }),
  ].filter(Boolean),
  ssr: {
    resolve: {
      externalConditions: ["workerd", "browser"],
    },
    noExternal: true,
  },
  test: {
    environment: "node",
    include: ["tests/**/*.test.ts", "tests/**/*.test.tsx"],
  },
});
