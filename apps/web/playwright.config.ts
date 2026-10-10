import { defineConfig, devices } from "@playwright/test";

const port = Number(process.env.DIFFX_TEST_PORT ?? 4173);

export default defineConfig({
  workers: 1,
  testDir: "./test/browser",
  outputDir: "test-results",
  use: {
    baseURL: `http://127.0.0.1:${port}`,
    trace: "retain-on-failure",
  },
  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"] },
    },
  ],
  webServer: {
    command: `pnpm dev --port ${port}`,
    url: `http://127.0.0.1:${port}`,
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
  },
});
