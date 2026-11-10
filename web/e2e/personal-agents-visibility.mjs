// Verify the actual web app against a mock hub, using only synthetic personal-agent responses.
// Start scripts/mock/mock_hub.py with MOCK_SESSIONS_LIST=1 and serve web with Vite, then:
// node web/e2e/personal-agents-visibility.mjs <web URL> --cdp <Chrome CDP URL> --shots <directory>
// Leaves the shared browser and its pages open. Every wait is bounded; failures exit nonzero.
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import assert from "node:assert/strict";
import { chromium } from "playwright-core";

const args = process.argv.slice(2);
const flag = (key) => args[args.indexOf(key) + 1];
const base = args[0];
const cdp = args.includes("--cdp") ? flag("--cdp") : "http://127.0.0.1:9222";
const shots = args.includes("--shots") ? flag("--shots") : undefined;
if (!base) throw new Error("usage: personal-agents-visibility.mjs <web URL> [--cdp <URL>] [--shots <directory>]");
if (shots) mkdirSync(shots, { recursive: true });
const browser = await chromium.connectOverCDP(cdp);
const fixture = { id: "example:planning", source: "example", sessionKey: "planning", accountId: "example-account", provider: "example", title: "Project planning", canSend: true, unreadCount: 0, rankTimeMs: 1 };

try {
  for (const theme of ["light", "dark"]) {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1100 }, locale: "zh-CN", colorScheme: theme });
    const page = await context.newPage();
    let setup = "disabled";
    let sessions = [fixture];
    let revision = 1;
    let pending = true;
    let release;
    const gate = new Promise((resolve) => { release = resolve; });
    await context.addInitScript((value) => {
      localStorage.setItem("persona.language", "zh-Hans");
      localStorage.setItem("persona.theme", value);
    }, theme);
    await context.route("**/api/v1/agents", async (route) => {
      if (pending) await gate;
      await route.fulfill({ json: { sources: [{ source: "example", enabled: true, accounts: [{ id: "example-account" }], ...(setup ? { setup } : {}) }] } });
    });
    await context.route("**/api/v1/agents/sessions*", async (route) => {
      const url = new URL(route.request().url());
      if (url.searchParams.has("counts")) {
        await route.fulfill({ json: { total: sessions.length, revision: String(revision), state: sessions.length ? { idle: sessions.length } : {}, machine: {}, project: {}, group: {} } });
      } else {
        await route.fulfill({ json: { sessions, total: sessions.length, has_more: false, revision: String(revision), remove: sessions.length ? [] : [fixture.id] } });
      }
    });
    await page.goto(`${base}/settings?token=mock`, { waitUntil: "domcontentloaded", timeout: 30000 });
    await page.waitForSelector("[data-sidebar-rows]", { timeout: 30000 });
    await page.getByPlaceholder("搜索设置", { exact: true }).waitFor({ state: "visible", timeout: 30000 });
    await page.waitForFunction(() => document.querySelector("main")?.textContent?.includes("主题色"), null, { timeout: 30000 });
    const section = page.locator("aside").getByText("个人 Agent", { exact: true });
    assert.equal(await section.count(), 0, "the cold loading entry must be absent");
    pending = false;
    release();
    const refresh = async () => {
      revision++;
      await page.evaluate(async () => {
        const agents = await import("/src/lib/agents.ts");
        agents.agentsChanged([]);
        await agents.refreshAgentSessions();
      });
    };
    await refresh();
    assert.equal(await section.count(), 0, "disabled setup must hide stale sessions");
    for (const state of ["missing", "no_accounts"]) {
      setup = state;
      await refresh();
      assert.equal(await section.count(), 0, `${state} must hide the entry`);
    }
    setup = "";
    sessions = [];
    await refresh();
    assert.equal(await section.count(), 0, "configured but empty must hide the entry");
    if (shots) await page.screenshot({ path: join(shots, `personal-agents-hidden-${theme}.png`) });
    sessions = [fixture];
    await refresh();
    await section.waitFor({ state: "visible", timeout: 15000 });
    await page.locator("aside").getByText(fixture.title, { exact: true }).waitFor({ state: "visible", timeout: 15000 });
    if (shots) await page.screenshot({ path: join(shots, `personal-agents-visible-${theme}.png`) });
    const loaded = await page.locator('[data-paged-sessions="personal"]').getAttribute("data-loaded-sessions");
    assert.equal(loaded, "1", "bounded list must contain a visible session");
    console.log(`PASS ${theme}: loading/missing/disabled/no_accounts/empty hidden; live configured session shown without reload`);
    await context.unrouteAll({ behavior: "wait" });
  }
  console.log("PASS personal-agents visibility browser verification (synthetic data; light and dark)");
  process.exit(0);
} catch (error) {
  console.error("FAIL personal-agents visibility browser verification:", error);
  process.exit(1);
}
