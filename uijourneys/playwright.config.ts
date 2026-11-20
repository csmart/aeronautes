import { defineConfig } from "@playwright/test";

// One application process serves every target, so the journeys run one at a
// time. Tracing and video are off, because a trace records what is typed; a
// failure keeps a screenshot and the page's text in the private directory,
// and the private reporter keeps failures out of the output (ADR 0112
// decision 7).
const targets = ["mock", ...(process.env.AERONAUTES_SAIO ? ["saio"] : []),
  ...(process.env.AERONAUTES_CLOUDS ? ["ceph", "swift"] : [])];

export default defineConfig({
  testDir: "tests",
  workers: 1,
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: 0,
  timeout: 10 * 60 * 1000,
  expect: { timeout: 60 * 1000 },
  outputDir: `${process.env.AERONAUTES_PRIVATE}/test-results`,
  reporter: [["./reporter.ts"]],
  globalTeardown: "./teardown.ts",
  use: {
    baseURL: process.env.AERONAUTES_UI,
    headless: true,
    actionTimeout: 30 * 1000,
    trace: "off",
    screenshot: "only-on-failure",
    video: "off",
  },
  projects: targets.map((name) => ({ name, metadata: { target: name } })),
});
