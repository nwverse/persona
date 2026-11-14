// Side sessions in the sidebar's Agents lists and their "Side sessions (N)" fold (#7755, docs/web/web.md "Paging hubs"):
// the two sessions the Claude desktop app started from the PR review are drawn under it, a level in, after the fold;
// folded, the side chat goes and the /btw question that waits for the user stays; the counts are the same open or
// folded, and Expand all opens it again. Checked in the complete list By repository (the fake hub pins a space by
// default, which keeps the sidebar on it) and in the paged list By machine (the runner clears the pins and puts them
// back). Screenshots open and folded, light and dark.
//
// Needs the web page against the fake hub with the paged list, its side sessions and one assistant, and Chrome:
//
//   MOCK_SESSIONS_LIST=1 MOCK_SIDE_SESSIONS=1 MOCK_MANAGERS=0 PORT=18795 python3 scripts/mock/mock_hub.py &
//   (cd web && PERSONA_HUB_URL=http://127.0.0.1:18795 npx vite --port 5195 --strictPort &)
//   node web/e2e/side-sessions-fold.mjs http://127.0.0.1:5195 <artifact directory> [--channel chrome]
//
// Prints one line per check and a verdict; exits 1 on a failed check and 2 on its three-minute timeout.
import { mkdirSync } from "node:fs";
import { chromium } from "playwright-core";

const args = process.argv.slice(2);
const flag = (name, fallback) => {
  const i = args.indexOf(name);
  return i >= 0 ? args[i + 1] : fallback;
};
const base = (args[0] ?? "").replace(/\/$/, "");
const out = args[1];
const channel = flag("--channel", "chrome");
if (!base || !out || out.startsWith("--")) {
  console.error("usage: node web/e2e/side-sessions-fold.mjs <web URL> <artifact directory> [--channel chrome]");
  process.exit(2);
}
mkdirSync(out, { recursive: true });
const watchdog = setTimeout(() => { console.log("VERDICT TIMEOUT side sessions fold"); process.exit(2); }, 180_000);

const failures = [];
const check = (ok, what, detail = "") => {
  console.log(`${ok ? "ok  " : "FAIL"} ${what}${detail ? `: ${detail}` : ""}`);
  if (!ok) failures.push(what);
};
const hubGet = async (path) => (await fetch(`${base}/api/v1${path}`, { headers: { Authorization: "Bearer mock" }, signal: AbortSignal.timeout(10_000) })).json();

const all = await hubGet("/sessions?summary=1&limit=200");
const rows = all.sessions ?? [];
const sides = rows.filter((s) => s.parent_agent_id);
const parent = rows.find((s) => sides[0] && s.session_id === sides[0].parent_agent_id);
const waiting = sides.find((s) => s.state === "needs_you");
const quiet = sides.find((s) => s.state !== "needs_you");
check(sides.length === 2 && Boolean(parent && waiting && quiet), "the fake hub lists two side sessions, one waiting, and their parent (MOCK_SIDE_SESSIONS=1)");

const hubPut = (path, body) => fetch(`${base}/api/v1${path}`, { method: "PUT", headers: { Authorization: "Bearer mock", "Content-Type": "application/json" }, body: JSON.stringify(body), signal: AbortSignal.timeout(10_000) });
const pins = await hubGet("/pins");

const browser = await chromium.launch({ channel, headless: true });
try {
  for (const [list, grouping, theme] of [["complete", "repo", "light"], ["complete", "repo", "dark"], ["paged", "machine", "light"], ["paged", "machine", "dark"]]) {
    const name = `${list}-${grouping}`;
    await hubPut("/pins", list === "paged" ? { spaces: [], sessions: [] } : pins);
    const viewport = { width: 1280, height: 1000 };
    const sidebar = 340;
    const ctx = await browser.newContext({ viewport, deviceScaleFactor: 2, locale: "en-US", colorScheme: theme });
    const page = await ctx.newPage();
    const errors = [];
    page.on("pageerror", (e) => errors.push(String(e)));
    await page.goto(`${base}/?token=mock`);
    await page.waitForFunction(() => localStorage.getItem("persona.token"), null, { timeout: 15_000 });
    await page.evaluate(([theme, sidebar, grouping]) => {
      localStorage.setItem("persona.theme", theme);
      localStorage.setItem("persona.language", "en");
      localStorage.setItem("persona.sidebarWidth", String(sidebar));
      localStorage.setItem("persona.agentGrouping", grouping);
      localStorage.removeItem("persona.agentChatList");
      for (const key of Object.keys(localStorage)) if (key.startsWith("fold:side-sessions:")) localStorage.removeItem(key);
    }, [theme, sidebar, grouping]);
    await page.goto(`${base}/`);
    const block = page.locator("[data-sidebar-agents]").first();
    await block.waitFor({ timeout: 20_000 });
    if (list === "paged") await block.locator("[data-paged-sessions]").first().waitFor({ timeout: 20_000 }).catch(() => {});
    check(await block.locator("[data-paged-sessions]").count() === (list === "paged" ? 1 : 0), `${name} ${theme}: the sidebar draws the ${list} list`);
    const link = (s) => block.locator(`a[href="/s/${encodeURIComponent(s.machine_id)}/${encodeURIComponent(s.session_id)}"]`).first();
    await link(parent).waitFor({ timeout: 20_000 });
    const fold = block.locator("[data-side-fold] button").first();
    // the rows of the review's group as drawn: in view (not in a folded part) and a level in
    const state = async () => Promise.all([parent, waiting, quiet].map(async (s) => {
      const a = link(s);
      if (!(await a.count())) return { title: s.title, shown: false, nested: false };
      return a.evaluate((el) => ({ title: el.textContent, shown: !el.closest("[inert]") && el.getClientRects().length > 0, nested: Boolean(el.closest("[data-nested-session]")) }));
    }));
    const counts = () => block.evaluate((el) => [...el.querySelectorAll("[data-sidebar-group-heading], [role=radiogroup] button")]
      .map((x) => (x.getAttribute("aria-label") ?? x.textContent ?? "").replace(/\s+/g, " ").trim()).join(" | "));

    let [p, w, q] = await state();
    check(p.shown && !p.nested && w.shown && w.nested && q.shown && q.nested, `${name} ${theme}: both side sessions are drawn a level in under the review`);
    check((await fold.textContent()) === `Side sessions (${sides.length})` && (await fold.getAttribute("aria-expanded")) === "true", `${name} ${theme}: the fold says how many and starts open`, await fold.textContent());
    const order = await block.evaluate((el, ids) => {
      const all = [...el.querySelectorAll("a[href^='/s/'], [data-side-fold] button")];
      return ids.map((id) => all.findIndex((x) => id === "fold" ? x.matches("[data-side-fold] button") : x.getAttribute("href")?.endsWith(`/${id}`)));
    }, [parent.session_id, "fold", waiting.session_id, quiet.session_id].map(encodeURIComponent));
    check(order.every((n, i) => n >= 0 && (i === 0 || n === order[0] + i)), `${name} ${theme}: the review, its fold, then its side sessions in a row`, order.join(","));
    const before = await counts();
    await link(parent).evaluate((el) => el.scrollIntoView({ block: "center" }));
    await page.mouse.move(viewport.width - 10, viewport.height - 10);
    await page.waitForTimeout(400);
    await page.screenshot({ path: `${out}/${name}-open-${theme}.png`, clip: { x: 0, y: 0, width: sidebar + 40, height: viewport.height } });

    await fold.click();
    await page.waitForTimeout(600);
    [p, w, q] = await state();
    check((await fold.getAttribute("aria-expanded")) === "false", `${name} ${theme}: one click folds them`);
    check(p.shown && w.shown && w.nested && !q.shown, `${name} ${theme}: folded, the side chat goes and the waiting question stays a row`);
    check((await counts()) === before, `${name} ${theme}: every count is the same folded`, before);
    check(await page.evaluate(() => Object.keys(localStorage).some((k) => k.startsWith("fold:side-sessions:") && localStorage.getItem(k) === "1")), `${name} ${theme}: the fold is kept`);
    await page.mouse.move(viewport.width - 10, viewport.height - 10);
    await page.waitForTimeout(200);
    await page.screenshot({ path: `${out}/${name}-folded-${theme}.png`, clip: { x: 0, y: 0, width: sidebar + 40, height: viewport.height } });

    // Expand all from the title row opens it again (after Collapse all, which folds the groups too)
    const foldAll = block.locator("[data-testid=agents-fold-all]").first();
    if ((await foldAll.getAttribute("aria-label"))?.startsWith("Collapse")) { await foldAll.click(); await page.waitForTimeout(600); }
    await foldAll.click();
    await page.waitForTimeout(600);
    [p, w, q] = await state();
    check((await fold.getAttribute("aria-expanded")) === "true" && q.shown, `${name} ${theme}: Expand all opens the fold`);
    check(errors.length === 0, `${name} ${theme}: no page errors`, errors.join("; "));
    await ctx.close();
  }
} finally {
  await browser.close();
  await hubPut("/pins", pins).catch(() => {});
  clearTimeout(watchdog);
}
console.log(`VERDICT ${failures.length ? "FAIL" : "PASS"} side sessions fold${failures.length ? `: ${failures.length} failed` : ""}; screenshots in ${out}`);
process.exit(failures.length ? 1 : 0);
