// The Agents list By project (knowledge base, lib/agent-projects.ts): the default grouping, Needs you across every
// project at the top, a section per project with its Agents by state (a sub-project rolled up into its project and
// named on the row), Unassigned last, and each row a short title with one line under it. Checked against the fake hub
// with its to-dos filed under projects; the project list is given a sub-project (天气 App › 小组件) and the to-do
// "排查 /orders 接口变慢" filed into it on the way to the page, and "登录页加验证码" stays in none. Light and dark, desktop and phone width.
//
//   MOCK_TODO_PROJECTS=1 PORT=18797 python3 scripts/mock/mock_hub.py &
//   (cd web && PERSONA_HUB_URL=http://127.0.0.1:18797 npx vite --port 5297 --strictPort &)
//   node web/e2e/agents-by-kb-project.mjs http://127.0.0.1:5297 <screenshot directory> [--channel chrome]
//
// Without --channel it drives Playwright's own Chromium. Prints one line per check and a verdict; exits 1 on a failed
// check and 2 on its two-minute timeout.
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { chromium } from "playwright-core";

const args = process.argv.slice(2);
const base = (args[0] ?? "").replace(/\/$/, "");
const out = args[1];
const ci = args.indexOf("--channel");
const channel = ci >= 0 ? args[ci + 1] : undefined;
if (!base || !out || out.startsWith("--")) {
  console.error("usage: node web/e2e/agents-by-kb-project.mjs <web URL> <screenshot directory> [--channel chrome]");
  process.exit(2);
}
mkdirSync(out, { recursive: true });
const watchdog = setTimeout(() => { console.log("VERDICT TIMEOUT agents by kb project"); process.exit(2); }, 120_000);
const failures = [];
const check = (ok, what, detail = "") => {
  console.log(`${ok ? "ok  " : "FAIL"} ${what}${detail ? `: ${detail}` : ""}`);
  if (!ok) failures.push(what);
};

// the fake hub pins a space by default, whose Agents the list shows under Pinned: cleared for the run, put back after
const hubCall = async (path, init) => (await fetch(`${base}/api/v1${path}`, { ...init, headers: { Authorization: "Bearer mock", "Content-Type": "application/json", ...init?.headers } })).json();
const savedPins = await hubCall("/pins");
await hubCall("/pins", { method: "PUT", body: JSON.stringify({ spaces: [], sessions: [] }) });
const browser = await chromium.launch({ channel, headless: true });
try {
  for (const [form, viewport] of [["desktop", { width: 1280, height: 1100 }], ["phone", { width: 390, height: 1100 }]]) {
    for (const theme of ["light", "dark"]) {
      const ctx = await browser.newContext({ viewport, deviceScaleFactor: 2, locale: "zh-CN", colorScheme: theme, reducedMotion: "reduce" });
      const page = await ctx.newPage();
      const errors = [];
      page.on("pageerror", (e) => errors.push(String(e)));
      // a sub-project under 天气 App, and one to-do in it
      await page.route("**/api/v1/kb/projects", async (route) => {
        const res = await route.fetch();
        const body = await res.json();
        body.items = [...(body.items ?? []), { kind: "projects", id: "weather-widgets", name: "小组件", parent: "weather-app", extra: "short · active" }];
        await route.fulfill({ response: res, json: body });
      });
      await page.route("**/api/v1/todos", async (route) => {
        if (route.request().method() !== "GET") return route.continue();
        const res = await route.fetch();
        const body = await res.json();
        for (const td of body.todos ?? []) if (td.id === "todo_orders") td.project = "weather-widgets";
        await route.fulfill({ response: res, json: body });
      });
      await page.goto(`${base}/?token=mock`);
      await page.waitForFunction(() => localStorage.getItem("persona.token"), null, { timeout: 15_000 });
      await page.evaluate((theme) => {
        localStorage.setItem("persona.theme", theme);
        localStorage.setItem("persona.language", "zh-Hans");
        localStorage.setItem("persona.sidebarWidth", "320");
        for (const key of Object.keys(localStorage)) if (key.startsWith("fold:") || key === "persona.agentGrouping") localStorage.removeItem(key);
      }, theme);
      // the phone's Agents page is the same block, full width
      await page.goto(form === "phone" ? `${base}/agents` : `${base}/`);
      const block = page.locator("[data-sidebar-agents]").first();
      await block.locator("[data-kbproject]").first().waitFor({ timeout: 20_000 });
      await page.waitForTimeout(500);
      const tag = `${form} ${theme}`;
      const checkedSeg = await block.locator('[role="radiogroup"][aria-label="Agent 分组"] [aria-checked="true"]').getAttribute("aria-label");
      check(checkedSeg === "按项目", `${tag}: 按项目 is the default`, checkedSeg ?? "");
      const needs = await block.locator("[data-kbproject-needs] a").evaluateAll((as) => as.map((a) => a.textContent ?? ""));
      check(needs.length === 1 && needs[0].includes("照片同步脚本"), `${tag}: Needs you is on top, naming the Agent's project`, JSON.stringify(needs));
      const heads = await block.locator("[data-kbproject]").evaluateAll((els) => els.map((el) => el.getAttribute("data-kbproject")));
      check(heads.includes("weather-app") && !heads.includes("weather-widgets"), `${tag}: the sub-project is rolled up into 天气 App`, heads.join(","));
      check(!heads.includes("unassigned") || heads.at(-1) === "unassigned", `${tag}: Unassigned, when there is one, is last`, heads.join(","));
      const weather = block.locator('[data-kbproject="weather-app"]');
      const sections = await weather.locator("[data-project-section]").evaluateAll((els) => els.map((el) => el.getAttribute("data-project-section")));
      check(sections.join(",") === "working", `${tag}: 天气 App by state`, sections.join(","));
      const sub = await weather.locator('[data-project-section="working"] a').first().textContent();
      check((sub ?? "").includes("› 小组件"), `${tag}: the sub-project's row names it`, sub ?? "");
      const photo = await block.locator('[data-kbproject="photo-sync"]').textContent();
      check((photo ?? "").includes("列在最上面"), `${tag}: a project whose Agents all need you points up to the strip`);
      const titles = await block.locator("[data-kbproject] a, [data-kbproject-needs] a").evaluateAll((as) => as.map((a) => a.querySelector("span.truncate")?.textContent ?? ""));
      check(titles.length >= 3 && titles.every((x) => x.length > 0 && x.length <= 24), `${tag}: every title is short`, JSON.stringify(titles));
      check(titles.includes("博客迁移到 Astro"), `${tag}: a to-do's only Agent is called by the to-do`);
      check(errors.length === 0, `${tag}: no page errors`, errors.join(" | "));
      const shot = join(out, `agents-by-project-${form}-${theme}.png`);
      if (form === "phone") await page.screenshot({ path: shot, fullPage: false });
      else await page.locator("aside").first().screenshot({ path: shot });
      console.log(`shot ${shot}`);
      await ctx.close();
    }
  }
} finally {
  await browser.close();
  await hubCall("/pins", { method: "PUT", body: JSON.stringify(savedPins) });
  clearTimeout(watchdog);
}
console.log(failures.length ? `VERDICT FAIL ${failures.length} check(s): ${failures.join("; ")}` : "VERDICT PASS agents by kb project");
process.exit(failures.length ? 1 : 0);
