// The console's smoke test in a real browser. Against a running stack, set
// ALERTROUTER_URL (the compose stack serves http://localhost:8080); without
// it, Playwright starts `next dev` on port 3210 with the embedded policies.
// Browsers come from Playwright's own installer into the project's cache
// (bun run ui:install); the spec files are *.pw.ts so `bun test` leaves
// them alone.
import { defineConfig, devices } from "@playwright/test";

const external = process.env.ALERTROUTER_URL;
// Not 3100: the compose stack's Loki listens there.
const port = 3210;

export default defineConfig({
  testDir: "test/ui",
  testMatch: "**/*.pw.ts",
  fullyParallel: false,
  forbidOnly: process.env.CI !== undefined,
  retries: process.env.CI !== undefined ? 1 : 0,
  reporter: process.env.CI !== undefined ? [["list"], ["html", { open: "never" }]] : "list",
  outputDir: "results/playwright",
  use: {
    baseURL: external ?? `http://localhost:${port}`,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
  webServer:
    external === undefined
      ? {
          command: `bun run dev --port ${port}`,
          url: `http://localhost:${port}/readyz`,
          reuseExistingServer: process.env.CI === undefined,
          timeout: 120_000,
        }
      : undefined,
});
