import { defineConfig } from "vitest/config";
import { svelte } from "@sveltejs/vite-plugin-svelte";
import wails from "@wailsio/runtime/plugins/vite";

export default defineConfig({
  server: {
    host: "127.0.0.1",
    port: Number(process.env.WAILS_VITE_PORT) || 9245,
    strictPort: true,
  },
  plugins: [svelte(), wails("./bindings")],
  // Component tests need svelte's client build, not the server one.
  resolve: process.env.VITEST ? { conditions: ["browser"] } : undefined,
  test: {
    environment: "jsdom",
    include: ["src/**/*.test.ts"],
    coverage: {
      // Whole-source accounting: untested files count as 0%, so the
      // number means coverage OF THE APPLICATION, not of whatever the
      // tests happened to import (review-wave test-gap note).
      all: true,
      include: ["src/**/*.{ts,svelte}"],
      exclude: ["src/**/*.test.ts"],
    },
  },
});
