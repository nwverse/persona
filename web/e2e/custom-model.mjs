// End-to-end check of typing a model ID on the web (#1644, docs/assistant/model-config.md "Typing a model ID"): "New
// agent"'s list shows the IDs typed before and "Other model…", whose field says why an ID can't be used and starts with
// a good one; a running Claude Code session's popover sends a typed ID through POST …/modes; the Persona chat's launcher
// menu takes one for the next messages.
//
// Needs the web page against the fake hub with MOCK_CUSTOM_MODELS=1, and Chrome (playwright-core drives it):
//
//   MOCK_CUSTOM_MODELS=1 PORT=18796 python3 scripts/mock/mock_hub.py &
//   (cd web && PERSONA_HUB_URL=http://127.0.0.1:18796 npx vite --port 5196 --strictPort &)
//   node web/e2e/custom-model.mjs http://127.0.0.1:5196 --hub http://127.0.0.1:18796 [--shots <dir>]
//
// Runs wide (1280), light and dark, in Chinese; --shots saves each state. Prints one line per check and a verdict;
// exits 1 on a failure.
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { chromium } from "playwright-core";

const args = process.argv.slice(2);
const flag = (name) => {
  const i = args.indexOf(name);
  return i >= 0 ? args[i + 1] : undefined;
};
const base = (args[0] ?? "").replace(/\/$/, "");
const hub = (flag("--hub") ?? "").replace(/\/$/, "");
const shots = flag("--shots");
if (!base || !hub) {
  console.error("usage: node web/e2e/custom-model.mjs <web URL> --hub <fake hub URL> [--shots <dir>]");
  process.exit(2);
}
if (shots) mkdirSync(shots, { recursive: true });

const REVIEW = "/s/macbook/a1f3c2d4-0000-4000-8000-000000000006"; // a resting Claude Code agent in a pane

const ok = (msg) => console.log(`ok  ${msg}`);
function fail(msg) {
  console.log(`FAIL ${msg}`);
  process.exit(1);
}

const mock = async (path) => (await fetch(`${hub}/mock/${path}`, { signal: AbortSignal.timeout(5000) })).json();

async function shot(page, name, theme) {
  if (!shots) return;
  await page.waitForTimeout(400); // transitions (a button turning on, a list folding) settle first
  await page.screenshot({ path: join(shots, `web-custom-model-${name}-${theme}.png`) });
}

async function expectVisible(page, locator, what) {
  try {
    await locator.first().waitFor({ state: "visible", timeout: 8000 });
  } catch {
    fail(`${what} (not visible)`);
  }
}

async function run(browser, theme) {
  await mock("custom_models?reset=1");
  const ctx = await browser.newContext({ viewport: { width: 1280, height: 820 }, deviceScaleFactor: 2, colorScheme: theme, locale: "zh-CN" });
  const page = await ctx.newPage();
  await page.goto(`${base}/?token=mock`);
  await page.waitForSelector("main textarea", { timeout: 15000 }); // paired
  await page.evaluate((t) => {
    localStorage.setItem("persona.theme", t);
    localStorage.setItem("persona.language", "zh-Hans");
  }, theme);
  await page.reload();
  await page.waitForSelector("main textarea", { timeout: 15000 });

  // New agent: the list, the field and its reason, the typed pick
  await page.locator('button[aria-label="新建 Agent"]').first().click();
  const dialog = page.getByRole("dialog");
  const trigger = dialog.locator("button[aria-expanded]").filter({ hasText: /默认|Opus|acme/ }).first();
  await expectVisible(page, trigger, `${theme}: New agent's model row`);
  await trigger.click();
  await expectVisible(page, dialog.getByText("输入过的"), `${theme}: the IDs typed before`);
  await expectVisible(page, dialog.getByRole("button", { name: "忘记「vendor/model-x」" }), `${theme}: Forget beside a typed ID`);
  const other = dialog.locator('[data-testid="model-picker-other"]');
  await other.scrollIntoViewIfNeeded();
  await shot(page, "new-agent-list", theme);
  ok(`${theme}: New agent lists the typed IDs and Other model…`);
  await other.click();
  const field = dialog.locator("[data-model-id-field] input");
  await field.fill("acme coder");
  await expectVisible(page, dialog.locator("[data-model-id-problem]").filter({ hasText: "空格" }), `${theme}: why "acme coder" can't be used`);
  await shot(page, "new-agent-invalid", theme);
  await field.fill("acme/coder-9");
  await field.press("Enter");
  await expectVisible(page, trigger.filter({ hasText: "acme/coder-9" }), `${theme}: the typed pick on the row`);
  await shot(page, "new-agent-picked", theme);
  ok(`${theme}: the field says why, and a good ID is the pick`);
  await page.keyboard.press("Escape");

  // A running session: Other model… in the popover, through …/modes (opened the way a click goes)
  await page.evaluate((path) => {
    history.pushState(null, "", path);
    dispatchEvent(new PopStateEvent("popstate"));
  }, REVIEW);
  const chip = page.locator('button[title="模型与思考强度"]').first();
  await expectVisible(page, chip, `${theme}: the session's model chip`);
  await chip.click();
  const popover = page.getByRole("dialog", { name: "模型与思考强度" });
  await expectVisible(page, popover.locator('[data-testid="model-popover-other"]'), `${theme}: Other model… in the popover`);
  await popover.locator('[data-testid="model-popover-other"]').click();
  const pf = popover.locator("[data-model-id-field] input");
  await pf.fill("a;b");
  await expectVisible(page, popover.locator("[data-model-id-problem]"), `${theme}: why "a;b" can't be used`);
  await pf.fill(`~vendor/model-${theme}`); // a model the session doesn't run yet (the fake hub keeps the last switch)
  if (await popover.locator("[data-model-id-field]").getByRole("button", { name: "使用" }).isDisabled()) fail(`${theme}: Use stays off for a good ID`);
  await shot(page, "session-popover", theme);
  await pf.press("Enter");
  await popover.getByRole("button", { name: "应用" }).click();
  for (let i = 0; i < 40; i++) {
    const { modes } = await mock("modes");
    if ((modes ?? []).some((m) => m.session_id === "a1f3c2d4-0000-4000-8000-000000000006" && m.model === `~vendor/model-${theme}`)) break;
    if (i === 39) fail(`${theme}: no POST …/modes with the typed model`);
    await page.waitForTimeout(150);
  }
  ok(`${theme}: the session switched through …/modes`);

  // The Persona chat: the launcher menu's Other model…
  await page.evaluate(() => {
    history.pushState(null, "", "/");
    dispatchEvent(new PopStateEvent("popstate"));
  });
  const launcherChip = page.locator('button[title="启动器：下一条消息起使用"]').first();
  await expectVisible(page, launcherChip, `${theme}: the chat's launcher chip`);
  await launcherChip.click();
  await page.getByRole("menu").getByText("claude", { exact: true }).first().click();
  await expectVisible(page, page.getByRole("menu").getByText("vendor/model-x"), `${theme}: a typed ID in the launcher's models`);
  await page.getByRole("menu").getByText("其他模型…").click();
  const mf = page.getByRole("menu").locator("input");
  await mf.fill("vendor model");
  await expectVisible(page, page.getByRole("menu").getByRole("alert"), `${theme}: why "vendor model" can't be used`);
  await shot(page, "chat-menu-invalid", theme);
  await mf.fill("vendor/model-y");
  await shot(page, "chat-menu-valid", theme);
  await mf.press("Enter");
  await expectVisible(page, page.getByText("vendor/model-y"), `${theme}: the pick said`);
  ok(`${theme}: the chat takes a typed ID for the next messages`);
  await ctx.close();
}

const browser = await chromium.launch({ channel: "chrome", headless: true });
try {
  for (const theme of ["light", "dark"]) await run(browser, theme);
} finally {
  await browser.close();
}
console.log("PASS custom model");
