import { existsSync } from "node:fs";
import { defineConfig, type PlaywrightTestConfig } from "@playwright/test";

const port = Number(process.env.PERSONA_UI_E2E_PORT || 4197);
const baseURL = `http://127.0.0.1:${port}`;

function resolveBrowserUse(): PlaywrightTestConfig["use"] {
  const executablePath = process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE;
  if (executablePath) {
    return { launchOptions: { executablePath, args: ["--no-sandbox"] } };
  }
  const channel = process.env.PLAYWRIGHT_CHANNEL;
  if (channel) {
    return { channel };
  }
  if (!process.env.CI && process.platform === "darwin" && existsSync("/Applications/Google Chrome.app/Contents/MacOS/Google Chrome")) {
    return { channel: "chrome" };
  }
  return { launchOptions: { args: ["--no-sandbox"] } };
}

export default defineConfig({
  testDir: "./e2e/ui",
  testMatch: "**/*.spec.ts",
  timeout: 30_000,
  expect: { timeout: 10_000 },
  fullyParallel: true,
  forbidOnly: Boolean(process.env.CI),
  retries: process.env.CI ? 1 : 0,
  workers: process.env.CI ? 2 : undefined,
  outputDir: "test-results/ui-artifacts",
  reporter: process.env.CI
    ? [["list"], ["html", { open: "never", outputFolder: "test-results/ui-report" }]]
    : [["list"]],
  use: {
    baseURL,
    viewport: { width: 1440, height: 900 },
    deviceScaleFactor: 2,
    locale: "en-US",
    reducedMotion: "reduce",
    serviceWorkers: "block",
    trace: "on-first-retry",
    screenshot: "only-on-failure",
    ...resolveBrowserUse(),
  },
  webServer: {
    command: `npx vite preview --host 127.0.0.1 --port ${port} --strictPort`,
    url: `${baseURL}/`,
    reuseExistingServer: false,
    timeout: 30_000,
  },
});
