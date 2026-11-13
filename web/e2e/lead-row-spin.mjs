// #6626 screenshots and DOM checks for the one-spinner rule on sidebar lead rows.
// The mock hub (MOCK_LEADERS=1) sends two directions with working=true and two with working=false. A second pass
// intercepts the tree and forces every working flag false, for the "nothing runs -> no spin" screenshots.
//   node web/e2e/lead-row-spin.mjs <web URL> --shots <directory> [--executable <chromium>]
import assert from "node:assert/strict";
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { chromium } from "playwright-core";

const args = process.argv.slice(2);
const flag = (name) => { const i = args.indexOf(name); return i < 0 ? undefined : args[i + 1]; };
const base = (args[0] ?? "").replace(/\/$/, "");
const shots = flag("--shots");
const executable = flag("--executable");
if (!base || !shots) {
  console.error("usage: node web/e2e/lead-row-spin.mjs <web URL> --shots <dir> [--executable <chromium>]");
  process.exit(2);
}
const origin = new URL(base);
assert(["127.0.0.1", "localhost", "[::1]"].includes(origin.hostname) && origin.protocol === "http:", "loopback only");
mkdirSync(shots, { recursive: true });
const watchdog = setTimeout(() => { console.error("TIMEOUT lead-row-spin (180s)"); process.exit(124); }, 180_000);

const results = [];
const ok = (line) => { results.push(line); console.log("ok:", line); };
const fail = (line) => { results.push("FAIL " + line); console.error("FAIL:", line); process.exitCode = 1; };

async function open(browser, theme, width, idle) {
  const ctx = await browser.newContext({ viewport: { width, height: width > 600 ? 820 : 844 }, deviceScaleFactor: 2, colorScheme: theme, locale: "zh-CN", hasTouch: width <= 600 });
  const page = await ctx.newPage();
  if (idle) {
    await page.route("**/api/v1/assistant/directions/tree", async (route) => {
      const res = await route.fetch();
      const body = await res.json();
      for (const d of body.directions ?? []) d.working = false;
      await route.fulfill({ response: res, json: body });
    });
  }
  await page.goto(`${base}/?token=mock`);
  await page.waitForFunction(() => localStorage.getItem("persona.token"), null, { timeout: 15000 });
  await page.evaluate((t) => {
    localStorage.setItem("persona.theme", t);
    localStorage.setItem("persona.language", "zh-Hans");
  }, theme);
  await page.goto(`${base}/overview`);
  await page.waitForSelector("main", { timeout: 15000 });
  if (width <= 600) {
    await page.locator("main").getByRole("button", { name: "侧边栏" }).first().click();
    await page.waitForTimeout(500); // the drawer slides in
  }
  await page.locator('aside [data-direction], aside a[href^="/persona/"]').first().waitFor({ state: "visible", timeout: 15000 });
  return { ctx, page };
}

// The lead rows under 常驻 (the mock's four direction names).
const NAMES = ["Persona 产品", "基础设施", "增长", "支付对账"];

for (const theme of ["light", "dark"]) {
  for (const width of [1280, 390]) {
    for (const idle of [false, true]) {
      const where = `${theme} ${width > 600 ? "wide" : "narrow"} ${idle ? "idle" : "busy"}`;
      const browser = await chromium.launch(executable ? { executablePath: executable } : undefined);
      const { ctx, page } = await open(browser, theme, width, idle);
      try {
        const rows = await page.locator('aside a[href^="/persona/"]').evaluateAll((els, names) => els
          .filter((a) => names.some((n) => a.textContent?.includes(n)))
          .map((a) => ({
            text: a.textContent ?? "",
            spin: !!a.querySelector('[data-shape="arc"][data-state="working"]'),
            needsYou: (a.textContent?.match(/等你 \d+/) ?? [null])[0],
            workingText: (a.textContent?.match(/在做 \d+/) ?? [null])[0],
          })), NAMES);
        if (rows.length !== NAMES.length) fail(`${where}: found ${rows.length} lead rows, want ${NAMES.length}`);
        for (const r of rows) {
          if (r.workingText) fail(`${where}: lead row still spells "${r.workingText}"`);
        }
        const spinning = rows.filter((r) => r.spin).length;
        const want = idle ? 0 : 2; // mock: Persona 产品 and 基础设施 run live Agents; 增长 and 支付对账 do not
        if (spinning !== want) fail(`${where}: ${spinning} spinning lead rows, want ${want}`);
        else ok(`${where}: ${spinning} lead rows spin, no row spells 在做 N`);
        const withNeedsYou = rows.filter((r) => r.needsYou).length;
        ok(`${where}: ${withNeedsYou} rows keep their 等你 N attention line`);
        await page.screenshot({ path: join(shots, `lead-row-spin-${width > 600 ? "wide" : "narrow"}-${theme}-${idle ? "idle" : "busy"}.png`), fullPage: false });
      } finally {
        await ctx.close();
        await browser.close();
      }
    }
  }
}

// Narrow width opens the drawer; the wide screenshots show the persistent sidebar.
clearTimeout(watchdog);
console.log(process.exitCode ? `\n${results.filter((r) => r.startsWith("FAIL")).length} check(s) failed` : "\nall checks passed");
process.exit(process.exitCode ?? 0);
