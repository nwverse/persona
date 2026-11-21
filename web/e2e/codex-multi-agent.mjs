// Synthetic browser verification for mixed Claude/Codex plans (#12082).
// MOCK_TASKS=1 MOCK_MANAGERS=1 MOCK_CODEX_MULTI_AGENT=1 PORT=18941 python3 scripts/mock/mock_hub.py
// (cd web && PERSONA_HUB_URL=http://127.0.0.1:18941 npm run dev -- --port 18942 --strictPort)
// node web/e2e/codex-multi-agent.mjs http://127.0.0.1:18942 --hub http://127.0.0.1:18941 [--shots <dir>]
import assert from "node:assert/strict";
import { mkdir } from "node:fs/promises";
import { join } from "node:path";
import { chromium } from "playwright-core";

function loopback(value) {
  const url = new URL(value);
  assert.equal(url.protocol, "http:", "Use a local test server");
  assert.equal(url.hostname, "127.0.0.1", "Use a loopback test server");
  assert.equal(url.pathname, "/");
  assert.equal(url.username + url.password + url.hash + url.search, "");
  return url.origin;
}

const args = process.argv.slice(2);
const option = (name) => args.includes(name) ? args[args.indexOf(name) + 1] : undefined;
const web = loopback(args[0]);
const hub = loopback(option("--hub"));
const shots = option("--shots");
if (shots) await mkdir(shots, { recursive: true });

const WORKFLOW = "Codex multi-agent";
const SKIPPED = "This Codex CLI does not support multi-agent v2. The Agent started without it.";
const AUTH = { Authorization: "Bearer mock" };
const layouts = [
  { tag: "wide-light", theme: "light", viewport: { width: 1280, height: 900 } },
  { tag: "phone-dark", theme: "dark", viewport: { width: 390, height: 844 } },
];

async function reset() {
  const response = await fetch(hub + "/mock/codex-multi-agent/reset", {
    method: "POST", headers: AUTH, redirect: "error", signal: AbortSignal.timeout(5000),
  });
  assert.equal(response.status, 200, "Load the mixed-workflow mock hub");
  return response.json();
}

const browser = await chromium.launch({ channel: "chrome", headless: true });
try {
  for (const { tag, theme, viewport } of layouts) {
    const fixture = await reset();
    const context = await browser.newContext({ viewport, locale: "en-US", colorScheme: theme, reducedMotion: "reduce", hasTouch: viewport.width < 600 });
    try {
      await context.route("**/*", (route) => new URL(route.request().url()).origin === web ? route.continue() : route.abort());
      await context.addInitScript((theme) => {
        localStorage.setItem("persona.token", "mock");
        localStorage.setItem("persona.language", "en");
        localStorage.setItem("persona.theme", theme);
      }, theme);
      const page = await context.newPage();
      page.setDefaultTimeout(15000);
      const errors = [];
      page.on("pageerror", (error) => errors.push(error.message));
      const capture = async (state) => {
        assert.deepEqual(errors, []);
        if (shots) await page.screenshot({ path: join(shots, "codex-multi-agent-" + tag + "-" + state + ".png"), animations: "disabled" });
      };

      await page.goto(web + "/");
      const actions = page.locator("[data-plan-actions]:not([data-assistant-actionbar] [data-plan-actions])").filter({ has: page.getByRole("button", { name: "Start 3 tasks", exact: true }) });
      const start = actions.getByRole("button", { name: "Start 3 tasks", exact: true });
      await start.waitFor();
      const card = actions.locator("..");
      const claude = card.getByRole("button", { name: "ultracode", exact: true });
      const codex = card.getByRole("button", { name: WORKFLOW, exact: true });
      await codex.nth(1).waitFor();
      assert.equal(await codex.count(), 2);
      assert.equal(await claude.isDisabled(), true, "The same machine has an older Claude CLI");
      for (const chip of await codex.all()) {
        assert.equal(await chip.getAttribute("aria-pressed"), "false");
        await chip.click();
        assert.equal(await chip.getAttribute("aria-pressed"), "true");
      }
      await card.scrollIntoViewIfNeeded();
      await capture("requested");

      const started = page.waitForResponse((response) => response.request().method() === "POST" &&
        new URL(response.url()).pathname === "/api/v1/task-plans/" + fixture.plan_id + "/start");
      await start.click();
      const response = await started;
      assert.equal(response.status(), 200);
      const edits = response.request().postDataJSON().tasks;
      assert.equal(edits.length, 3);
      assert.notEqual(edits[0].ultracode, true);
      assert.deepEqual(edits.slice(1), [{ ultracode: true }, { ultracode: true }]);
      const { plan } = await response.json();
      assert.equal(plan.tasks[0].workflow_active, undefined);
      assert.equal(plan.tasks[1].workflow_active, true);
      assert.equal(plan.tasks[2].workflow_active, undefined);
      assert.equal(plan.tasks[2].workflow_note, SKIPPED);
      const chat = page.locator("[data-chat-scroll]");
      await start.waitFor({ state: "hidden" });
      await chat.getByText(WORKFLOW, { exact: false }).first().waitFor();
      const older = chat.getByText(SKIPPED, { exact: false });
      await older.first().waitFor();
      await older.first().scrollIntoViewIfNeeded();
      await capture("receipts");

      await page.goto(web + "/agents");
      const confirmed = page.locator('[data-testid="agent-chatlist-row"][data-session-id="' + fixture.confirmed + '"]');
      const legacy = page.locator('[data-testid="agent-chatlist-row"][data-session-id="' + fixture.legacy + '"]');
      await confirmed.getByRole("img", { name: WORKFLOW, exact: true }).waitFor();
      await legacy.waitFor();
      assert.equal(await legacy.getByRole("img", { name: WORKFLOW, exact: true }).count(), 0);
      await capture("agents");

      await confirmed.getByRole("link").click();
      const pill = page.locator('button[data-model-pill="ready"]').first();
      await pill.click();
      const panel = page.locator("[data-model-panel]");
      const model = panel.locator("[data-panel-row][data-current]").first();
      await model.waitFor();
      if (viewport.width >= 600) await model.hover();
      await model.locator("[data-row-customize]").click({ force: true });
      const flyout = page.locator("[data-model-flyout]");
      await flyout.waitFor();
      assert.equal(await flyout.getByRole("switch", { name: /ultracode|multi-agent/i }).count(), 0);
      await capture("codex-panel");
      console.log("ok  " + tag + ": request, start receipts, Agent marks and Codex model controls");
    } finally {
      await context.close();
    }
  }
} finally {
  await browser.close();
}
