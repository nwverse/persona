// Screenshots and checks of "New agent" graying out a launcher this computer's terminal can't run (#1643), and of a chat
// whose start the node refused with a code (the hub's whole sentence under "Couldn't start", #1642).
//
// Needs the web page against the fake hub with MOCK_LAUNCHER_HEALTH=1, and Chrome (playwright-core drives it):
//
//   MOCK_LAUNCHER_HEALTH=1 PORT=18795 python3 scripts/mock/mock_hub.py &
//   (cd web && PERSONA_HUB_URL=http://127.0.0.1:18795 npx vite --port 5195 --strictPort &)
//   node web/e2e/launcher-health.mjs http://127.0.0.1:5195 [--shots <dir>] [--locale zh-CN|en-US|ja-JP]
//
// Runs wide (1280) and phone width (390), light and dark: the dialog with work_gw (not in the terminal) and team_gw (its key
// rejected) grayed out and said under the launchers, work_gw's "How to fix", and the failed chat. Prints one line per
// check and a verdict; exits 1 on a failure.
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { chromium } from "playwright-core";

const args = process.argv.slice(2);
const flag = (name) => {
  const i = args.indexOf(name);
  return i >= 0 ? args[i + 1] : undefined;
};
const base = (args[0] ?? "").replace(/\/$/, "");
const shots = flag("--shots");
const locale = flag("--locale") ?? "zh-CN";
if (!base) {
  console.error("usage: node web/e2e/launcher-health.mjs <web URL> [--shots <dir>] [--locale zh-CN|en-US|ja-JP]");
  process.exit(2);
}
if (shots) mkdirSync(shots, { recursive: true });

const FAILED = "/s/macbook/f1643000-0000-4000-8000-000000000001"; // MOCK_LAUNCHER_HEALTH's refused start
const SIZES = { wide: { width: 1280, height: 860 }, narrow: { width: 390, height: 844 } };
const RUNS = [["wide", "light"], ["wide", "dark"], ["narrow", "light"], ["narrow", "dark"]];

const ok = (msg) => console.log(`ok  ${msg}`);
function fail(msg) {
  console.log(`FAIL ${msg}`);
  process.exit(1);
}

async function shot(page, name, tag) {
  if (!shots) return;
  await page.waitForTimeout(400); // the dialog's and the help's entrances
  await page.screenshot({ path: join(shots, `${name}-${tag}.png`) });
}

const sideways = (page) => page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);

async function openNewAgent(page, tag, narrow) {
  // the sidebar's (or, at phone width, the agent list's) New agent button
  const name = /^(新建 Agent|New agent|新規エージェント)$/;
  let add = page.getByRole("button", { name }).filter({ visible: true });
  if (narrow) {
    // at phone width the sidebar is a drawer off the screen: the Agents page has the button
    await page.goto(`${base}/agents`);
    add = page.locator("main").getByRole("button", { name }).filter({ visible: true });
  }
  await add.first().click({ timeout: 8000 }).catch(async (e) => {
    if (shots) await page.screenshot({ path: join(shots, `failed-${tag}.png`) });
    fail(`${tag}: no New agent button (${String(e).split("\n")[0]})`);
  });
  await page.locator('[role="dialog"]').waitFor({ timeout: 8000 }).catch(() => fail(`${tag}: the New agent dialog did not open`));
}

async function run(browser, size, theme) {
  const tag = `${size}-${theme}`;
  const ctx = await browser.newContext({ viewport: SIZES[size], deviceScaleFactor: 2, hasTouch: size === "narrow", colorScheme: theme, locale });
  const page = await ctx.newPage();
  await page.goto(`${base}/?token=mock`);
  await page.waitForSelector("main textarea", { timeout: 15000 }).catch(() => fail(`${tag}: did not pair with the fake hub`));
  await page.evaluate((t) => localStorage.setItem("persona.theme", t), theme);
  await page.reload();
  await page.waitForSelector("main textarea", { timeout: 15000 });

  await openNewAgent(page, tag, size === "narrow");
  const blocked = page.locator('[data-testid="new-agent-blocked"]');
  await blocked.waitFor({ timeout: 8000 }).catch(() => fail(`${tag}: no line under the launchers for the grayed-out ones`));
  const lines = await blocked.locator("li").count();
  if (lines !== 2) fail(`${tag}: ${lines} grayed-out launchers said, want work_gw and team_gw`);
  ok(`${tag}: work_gw and team_gw are said under the launchers`);
  const disabled = await page.locator('button[aria-disabled="true"]').evaluateAll((els) => els.map((e) => e.textContent?.trim()));
  if (!disabled.includes("work_gw") || !disabled.includes("team_gw")) fail(`${tag}: grayed-out chips ${JSON.stringify(disabled)}`);
  const picked = await page.locator('button[aria-pressed="true"][aria-disabled="true"]').count();
  if (picked) fail(`${tag}: a grayed-out launcher is picked by default`);
  ok(`${tag}: both chips are grayed out and neither is picked`);
  if ((await sideways(page)) > 0) fail(`${tag}: the page scrolls sideways`);
  await shot(page, "web-newagent", tag);

  await blocked.locator("li").first().locator("button").click();
  const help = page.locator('[data-testid="launcher-blocked-help"]');
  await help.waitFor({ timeout: 4000 }).catch(() => fail(`${tag}: How to fix did not open`));
  const text = (await help.textContent()) ?? "";
  if (!text.includes("work_gw") || !text.includes("~/.bashrc")) fail(`${tag}: the help does not say what to do: ${text.slice(0, 120)}`);
  ok(`${tag}: How to fix says where to define it`);
  await help.scrollIntoViewIfNeeded();
  await shot(page, "web-newagent-help", tag);

  // a fresh load: the dialog may still be leaving through history (phone width)
  await page.goto(base + FAILED).catch(() => page.goto(base + FAILED));
  const coded = page.locator('[data-testid="start-error-coded"]');
  await coded.waitFor({ timeout: 8000 }).catch(() => fail(`${tag}: the chat does not show the coded start failure`));
  const sentence = (await coded.textContent()) ?? "";
  if (!sentence.includes("work_gw") || !sentence.includes("模型供应商")) fail(`${tag}: the sentence is cut or missing: ${sentence}`);
  ok(`${tag}: the chat shows the hub's whole sentence`);
  await shot(page, "web-chat-coded", tag);
  await ctx.close();
}

const browser = await chromium.launch({ channel: "chrome", headless: true });
try {
  for (const [size, theme] of RUNS) await run(browser, size, theme);
} finally {
  await browser.close();
}
console.log("PASS launcher health");
