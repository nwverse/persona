// Settings › This hub › Partner hubs in the real web app against an isolated mock hub (MOCK_PARTNER_HUBS=1, synthetic).
// node web/e2e/partner-hubs.mjs <web URL> --hub <mock hub URL> --shots <directory> [--lang zh-Hans|en|ja]
// Checks the two partners' rows (state, line, switches), a switch saving through the mock and showing its answer, Unpair's
// in-row confirmation, Show my code and Pair… with a pasted code; light and dark, wide and phone; screenshots of the group
// and both dialogs. The mock's list is put back afterwards (GET /mock/partner_hubs?reset=1).
import assert from "node:assert/strict";
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { chromium } from "playwright-core";
const args = process.argv.slice(2);
const option = (name) => args[args.indexOf(name) + 1];
const base = args[0];
const hub = args.includes("--hub") ? option("--hub") : undefined;
const shots = args.includes("--shots") ? option("--shots") : undefined;
const lang = args.includes("--lang") ? option("--lang") : "zh-Hans";
if (!base || !hub || !shots) throw new Error("Usage: <web URL> --hub <mock hub URL> --shots <directory> [--lang zh-Hans|en|ja]");
mkdirSync(shots, { recursive: true });
const watchdog = setTimeout(() => { console.error("TIMEOUT partner-hubs web E2E after 120 seconds"); process.exit(1); }, 120000);
const mock = async (query = "") => (await fetch(`${hub}/mock/partner_hubs${query}`)).json();
const words = {
  "zh-Hans": { title: "协作 hub", connected: "已连接", offline: "离线", confirm: "发送前问我", dial: "主动连接这个 hub", unpair: "解除配对", cancel: "取消", showCode: "我的配对码", pair: "配对…", pairButton: "配对", done: "完成" },
  en: { title: "Partner hubs", connected: "Connected", offline: "Offline", confirm: "Ask me before sending", dial: "Connect to this hub", unpair: "Unpair", cancel: "Cancel", showCode: "Show my code", pair: "Pair…", pairButton: "Pair", done: "Done" },
  ja: { title: "パートナーハブ", connected: "接続済み", offline: "オフライン", confirm: "送信前に確認", dial: "このハブに接続する", unpair: "ペアリング解除", cancel: "キャンセル", showCode: "コードを表示", pair: "ペアリング…", pairButton: "ペアリング", done: "完了" },
}[lang];
const pasted = `persona://partner?hub=9d0c2b1a-0000-4000-8000-00000000000d&name=Studio&fp=c3R1ZGlvLWZpbmdlcnByaW50LWZvci10aGUtc2NyZWVu&tls=18662&url=https%3A%2F%2Fhub.example.com%3A18662&code=jh_${"ab".repeat(32)}`;
const browser = await chromium.launch({ channel: "chrome", headless: true });
let activePage;
try {
  await mock("?reset=1");
  for (const width of [1280, 390]) for (const theme of ["light", "dark"]) {
    const context = await browser.newContext({ viewport: { width, height: 1000 }, locale: lang === "en" ? "en-US" : lang === "ja" ? "ja-JP" : "zh-CN", colorScheme: theme });
    const page = await context.newPage(); activePage = page;
    page.setDefaultTimeout(15000);
    const errors = []; page.on("pageerror", (e) => errors.push(e.message));
    await context.addInitScript(({ scheme, lang }) => { localStorage.setItem("persona.theme", scheme); localStorage.setItem("persona.language", lang); }, { scheme: theme, lang });
    await page.goto(`${base}/settings?tab=hub&token=mock`);
    const group = page.locator('[data-settings-group="partner-hubs"]');
    await group.locator("[data-partner-hub]").first().waitFor();
    await page.evaluate((scheme) => document.documentElement.classList.toggle("dark", scheme === "dark"), theme);
    await group.scrollIntoViewIfNeeded();
    const rows = group.locator("[data-partner-hub]");
    assert.equal(await rows.count(), 2);
    assert.match(await rows.nth(0).innerText(), new RegExp(words.connected));
    assert.match(await rows.nth(1).innerText(), new RegExp(words.offline));
    const ask = (i) => rows.nth(i).getByRole("switch", { name: words.confirm });
    assert.equal(await ask(0).getAttribute("aria-checked"), "true");
    assert.equal(await ask(1).getAttribute("aria-checked"), "false");
    assert.equal(await rows.nth(1).getByRole("switch", { name: words.dial }).getAttribute("aria-checked"), "false");
    // a switch shows what the mock answered
    await ask(1).click();
    await page.waitForFunction(() => document.querySelectorAll('[data-partner-hub]')[1]?.querySelector('[role="switch"]')?.getAttribute("aria-checked") === "true");
    assert.equal(Object.values((await mock()).prefs).filter((v) => v === true).length, 1);
    await ask(1).click();
    await page.waitForFunction(() => document.querySelectorAll('[data-partner-hub]')[1]?.querySelector('[role="switch"]')?.getAttribute("aria-checked") === "false");
    // Unpair asks in the row; Cancel leaves it
    await rows.nth(1).getByRole("button", { name: words.unpair, exact: true }).click();
    await rows.nth(1).getByRole("button", { name: words.cancel, exact: true }).waitFor();
    await rows.nth(1).getByRole("button", { name: words.cancel, exact: true }).click();
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth), "no horizontal overflow");
    const tag = `${lang}-${width === 1280 ? "wide" : "narrow"}-${theme}`;
    await group.screenshot({ path: join(shots, `partner-hubs-${tag}.png`) });
    // Show my code
    await group.getByRole("button", { name: words.showCode, exact: true }).click();
    // the open one: a dialog that just closed stays in the page while it leaves
    const open = page.locator('[role="dialog"][aria-modal="true"]');
    const codeDialog = open.locator("[data-partner-code-dialog]");
    await codeDialog.getByText("persona://partner?", { exact: false }).waitFor();
    await open.screenshot({ path: join(shots, `partner-hubs-code-${tag}.png`) });
    await open.getByRole("button", { name: words.done, exact: true }).click();
    // Pair… with a pasted code: the hub's name and fingerprint before Pair
    await group.getByRole("button", { name: words.pair, exact: true }).click();
    await open.locator("textarea").fill(pasted);
    await open.locator("[data-partner-code-facts]").waitFor();
    assert.match(await open.locator("[data-partner-code-facts]").innerText(), /Studio/);
    assert.equal(await open.getByRole("button", { name: words.pairButton, exact: true }).isEnabled(), true);
    await open.screenshot({ path: join(shots, `partner-hubs-pair-${tag}.png`) });
    await open.getByRole("button", { name: words.cancel, exact: true }).click();
    await page.waitForFunction(() => !document.querySelector("vite-error-overlay"));
    assert.deepEqual(errors, []);
    console.log(`PASS ${tag}: partners=2 switch_saved=ok unpair_confirm=ok code=ok pair_facts=ok console_errors=0`);
    await context.close();
  }
  await mock("?reset=1");
  console.log("PASS partner-hubs web E2E (synthetic mock only)");
} catch (error) {
  if (activePage) console.error((await activePage.locator("body").innerText()).slice(0, 2500));
  console.error(`FAIL partner-hubs web E2E: ${error.message}`); process.exitCode = 1;
} finally { await browser.close(); clearTimeout(watchdog); }
