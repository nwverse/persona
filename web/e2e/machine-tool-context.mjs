// Review captures for the Machines overview and machine-specific routes. Run against the loopback mock hub
// with MOCK_MANAGERS=0, two online computers named Computer A and Computer B, and a waiting session.
// node web/e2e/machine-tool-context.mjs <local web URL> <output directory> (CHROME_BIN overrides Chrome).
import assert from "node:assert/strict";
import { mkdir } from "node:fs/promises";
import { join } from "node:path";
import { chromium } from "playwright-core";

const [url, out] = process.argv.slice(2);
if (!out || !["localhost", "127.0.0.1", "[::1]"].includes(new URL(url).hostname)) {
  throw new Error("Supply the local mock web URL and a screenshot directory outside the repository.");
}
await mkdir(out, { recursive: true });
const browser = await chromium.launch(process.env.CHROME_BIN
  ? { executablePath: process.env.CHROME_BIN, args: ["--no-sandbox"] }
  : { channel: "chrome" });
const timeout = setTimeout(() => { console.error("TIMEOUT: Machines browser verification"); process.exit(2); }, 360_000);

async function verify(width, theme, textSize) {
  const height = width === 390 ? 844 : 900;
  const context = await browser.newContext({ viewport: { width, height }, hasTouch: width === 390, colorScheme: theme });
  try {
    await context.addInitScript(({ theme, textSize }) => {
      localStorage.setItem("persona.theme", theme);
      localStorage.setItem("persona.language", "en");
      localStorage.setItem("persona.text-size", textSize);
    }, { theme, textSize });
    const page = await context.newPage();
    const errors = [];
    page.on("pageerror", (e) => errors.push(e.message));
    await page.route("**/api/v1/machines/studio/files/roots", r => r.fulfill({ json: { roots: [] } }));
    await page.route("**/api/v1/machines/studio/files/list*", r => r.fulfill({ json: {
      path: "/projects", canonical_path: "/projects", parent: "/", total: 1,
      entries: [{ name: "Notes", path: "/projects/Notes", kind: "dir", mode: "drwxr-xr-x", size: 0 }],
    } }));
    await page.route("**/api/v1/machines/studio/storage", r => r.fulfill({ json: {
      supported: true, os: "darwin", mounts: [{ mount: "/", device: "Local disk", fs_type: "apfs",
        total_bytes: 500e9, used_bytes: 200e9, avail_bytes: 300e9, used_percent: 40 }],
    } }));
    await page.route("**/api/v1/machines/*/network", (r) => r.fulfill({ json: {
      interfaces: [{ name: "Ethernet", up: true, addresses: [] }], proxy: { active: false },
      probes: [{ name: "Hub connection", target: "example.com", protocol: "https", reachable: true, latency_ms: 24, status_code: 200 }],
    } }));
    await page.goto(new URL("/settings?token=mock", url).href);
    await page.waitForSelector("aside, nav");
    await page.goto(new URL("/machines", url).href);

    const detail = page.getByRole("heading", { level: 1, name: "Computer B", exact: true });
    const nav = page.getByRole("navigation", { name: "View", exact: true });
    const rail = page.getByRole("navigation", { name: "Machines", exact: true });
    const capture = async view => {
      await page.evaluate(() => document.fonts.ready);
      assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1));
      await page.screenshot({ path: join(out, `${width}-${theme}-${textSize}-${view}.png`), animations: "disabled" });
    };

    const computer = page.getByRole("link").filter({ has: page.getByRole("heading", { name: "Computer B", exact: true }) });
    await computer.waitFor();
    await capture("fleet");
    await computer.click();
    await detail.waitFor();
    const workPath = new URL(page.url()).pathname;
    assert.match(workPath, /^\/machines\/[^/]+$/);
    assert.equal(await rail.isVisible(), width === 1440);
    if (width === 1440) {
      await rail.getByRole("link", { name: /Computer A/ }).click();
      await page.getByRole("heading", { level: 1, name: "Computer A", exact: true }).waitFor();
      await rail.getByRole("link", { name: /Computer B/ }).click();
      await detail.waitFor();
    }
    await capture("work");
    for (const [view, label] of [["files", "Files"], ["storage", "Storage"], ["diagnostics", "Diagnostics"]]) {
      await nav.getByRole("link", { name: label, exact: true }).click();
      await page.waitForURL(u => u.pathname === workPath + "/" + view);
      await detail.waitFor();
      await nav.locator('[aria-current="page"]').filter({ hasText: label }).waitFor();
      if (view === "storage") await page.getByText("Local disk", { exact: false }).first().waitFor();
      if (view === "files") await page.getByTestId("machine-file-browser").getByText("Notes", { exact: true }).waitFor();
      await capture(view);
    }
    await page.getByRole("link", { name: "Network", exact: true }).click();
    await page.getByText("Hub connection", { exact: true }).waitFor();
    await page.reload();
    await page.getByText("Hub connection", { exact: true }).waitFor();
    await detail.waitFor();
    assert.equal(new URL(page.url()).pathname, workPath + "/network");
    await capture("network");
    await page.getByRole("link", { name: "Back", exact: true }).click();
    await page.waitForURL(u => u.pathname === workPath + "/diagnostics");
    await page.goForward();
    await page.waitForURL(u => u.pathname === workPath + "/network");
    if (width === 390) {
      for (const path of ["/diagnostics", "/storage", "/files", ""]) {
        await page.getByRole("link", { name: "Back", exact: true }).click();
        await page.waitForURL(u => u.pathname === workPath + path);
      }
      await page.getByRole("link", { name: "Back", exact: true }).click();
      await page.waitForURL(u => u.pathname === "/machines");
      await computer.waitFor();
    }
    assert.deepEqual(errors, []);
    console.log(`PASS ${width}×${height} ${theme} ${textSize}: fleet, computer, Files, Storage, Diagnostics, Network, reload and history`);
  } finally {
    await context.close();
  }
}
try {
  for (const theme of ["light", "dark"]) {
    for (const width of [1440, 390]) {
      for (const textSize of ["standard", "large"]) await verify(width, theme, textSize);
    }
  }
  console.log("PASS: Machines browser verification");
} finally {
  await browser.close();
  clearTimeout(timeout);
}
