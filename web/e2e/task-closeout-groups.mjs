// The decided Needs you / In progress entries in the real web app, with synthetic HTTP/WS only and no connection to a user's hub.
// Checks closeout versus acceptance, external waits, phone bounds, both themes and large translated text.
// node web/e2e/task-closeout-groups.mjs <web URL> --shots <directory> [--chromium <executable>]
import assert from "node:assert/strict";
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { chromium } from "playwright-core";

const args = process.argv.slice(2);
const flag = (name) => { const i = args.indexOf(name); return i < 0 ? undefined : args[i + 1]; };
const base = args[0]?.replace(/\/$/, "");
const shots = flag("--shots");
if (!base || !shots) {
  console.error("usage: node web/e2e/task-closeout-groups.mjs <web URL> --shots <directory> [--chromium <executable>]");
  process.exit(2);
}
const origin = new URL(base);
assert(["127.0.0.1", "localhost", "[::1]"].includes(origin.hostname) && origin.protocol === "http:", "only an owned loopback HTTP web server is allowed");
assert(!origin.username && !origin.password && !origin.search && !origin.hash, "the web URL must not contain credentials");
mkdirSync(shots, { recursive: true });
const watchdog = setTimeout(() => { console.error("TIMEOUT task-closeout-groups browser verification (180s)"); process.exit(124); }, 180_000);
const sid = "task-closeout-demo";
const readings = [];
const words = {
  en: { groups: ["Needs you", "In progress"],
    done: "Reported done · awaiting acceptance", unfinished: "Stopped · unfinished", hint: "Supervisor: confirm the wait and park this task" },
  "zh-Hans": { groups: ["要你处理", "进行中"],
    done: "做完了·待验收", unfinished: "停下·没做完", hint: "请督办确认等待并挂搁置" },
  ja: { groups: ["要対応", "進行中"],
    done: "完了報告・確認待ち", unfinished: "停止・未完了", hint: "監督役：待機を確認して保留してください" },
};

async function run(browser, size, theme, language, textSize = "standard") {
  const context = await browser.newContext({ viewport: size === "wide" ? { width: 1280, height: 900 } : { width: 390, height: 844 },
    colorScheme: theme, reducedMotion: "reduce" });
  await context.addInitScript(({ theme, language, textSize }) => {
    localStorage.setItem("persona.token", "synthetic-demo-token");
    localStorage.setItem("persona.language", language);
    localStorage.setItem("persona.theme", theme);
    localStorage.setItem("persona.text-size", textSize);
  }, { theme, language, textSize });
  const at = new Date(Date.now() - 15 * 60_000).toISOString();
  const old = new Date(Date.now() - 61 * 60_000).toISOString();
  const task = (id, state = "") => ({ id, title: `Example ${id}`, origin: sid, stage: "review", created_at: at, updated_at: at,
    agents: [{ machine_id: "demo", session_id: id, linked_at: at }], supervision: { supervisor: sid, state } });
  const list = [task("owner", "needs_user"), task("running"), task("reported", "pending_review"), task("unfinished", "rework"),
    task("external"), task("missing"), task("parked", "parked"), task("unattended"), task("named-wait", "pending_review")];
  list[6].supervision.parked = { reason: "Waiting for the maintainer re-review", waiting_on: "someone_else", count: 1,
    until: new Date(Date.now() + 24 * 3600_000).toISOString() };
  list[7].created_at = old;
  list[7].agents[0].linked_at = old;
  list[8].created_at = old;
  list[8].agents[0].linked_at = old;
  const session = { machine_id: "demo", session_id: sid, title: "Example task review", assistant: true, state: "idle", status: "idle", updated_at: at };
  const agents = list.map((t) => ({ machine_id: "demo", session_id: t.id, title: `${t.title} Agent`, cwd: "/example",
    state: t.id === "running" ? "working" : t.id === "reported" || t.id === "unfinished" ? "unread" : "idle",
    status: t.id === "running" ? "working" : "idle", updated_at: at, state_since: at,
    work: t.id === "missing" ? undefined : { phase: t.id === "running" ? "working" : ["external", "parked", "named-wait"].includes(t.id) ? "blocked" : "",
      on: ["external", "parked", "named-wait"].includes(t.id) ? "external" : undefined,
      wait: t.id === "named-wait" ? "the release checks finish" : undefined,
      turn_at: ["unattended", "named-wait"].includes(t.id) ? old : at,
      closeout: { status: t.id === "reported" ? "done" : ["external", "parked", "named-wait"].includes(t.id) ? "blocked" : "not_done",
        waiting: "the maintainer to re-review the release" } } }));
  const apiReads = [];
  await context.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname.replace(/^\/api\/v1/, "");
    if (request.method() !== "GET") { // the app's own bookkeeping (read marks); a parked task has nothing to press
      assert(!path.startsWith("/todos"), `nothing is written to a to-do: ${request.method()} ${path}`);
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ ok: true }) });
      return;
    }
    apiReads.push(path);
    let body = {};
    if (path === "/machines") body = { machines: [{ id: "demo", name: "Example computer", online: true, os: "linux" }] };
    else if (path === "/sessions") body = { sessions: [session, ...agents] };
    else if (path === "/interactions") body = { interactions: [] };
    else if (path === "/assistant") body = { machine_id: "demo", main: sid, conversations: [session], tasks: [], memory: [] };
    else if (path === "/todos") body = { todos: list };
    else if (path === "/assistant/repositories") body = { manager: request.headers()["x-persona-manager"] || "assistant", catalog_revision: 0, repositories: [] };
    else if (/^\/todos\/[^/]+\/repositories$/.test(path)) body = { schema_version: 1, manager: request.headers()["x-persona-manager"] || "assistant", todo_id: decodeURIComponent(path.split("/")[2]), revision: 0, repositories: [] };
    else if (path === `/sessions/demo/${sid}`) body = { session };
    else if (path.endsWith("/feed")) body = { items: [] };
    else if (path.endsWith("/items")) body = { items: [], has_more: false };
    else if (path === "/extensions/ui") body = { slots: [] };
    else if (path === "/extensions") body = { extensions: [] };
    else if (path === "/managers") body = { managers: [] };
    await route.fulfill({ contentType: "application/json", body: JSON.stringify(body) });
  });
  await context.routeWebSocket("**/api/v1/ws**", (ws) => {
    ws.send(JSON.stringify({ type: "hello", server_version: "synthetic", hub_name: "Example hub", features: [] }));
    ws.onMessage((raw) => {
      const message = JSON.parse(String(raw));
      if (message.type === "app_ping") ws.send(JSON.stringify({ type: "pong" }));
    });
  });
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", (error) => errors.push(String(error)));
  page.setDefaultTimeout(12_000);
  const name = (step) => join(shots, `closeout-${size}-${theme}-${language}-${textSize}-${step}.png`);
  const noOverflow = async () => assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth), false, "no horizontal page overflow");
  try {
    await page.goto(`${base}/`);
    const entries = page.locator("[data-tray-entry]");
    await entries.last().waitFor();
    assert.deepEqual(await entries.evaluateAll((els) => els.map((el) => el.dataset.trayEntry)), ["needs", "running"]);
    const labels = await entries.allTextContents();
    words[language].groups.forEach((label, i) => assert(labels[i].startsWith(label), `group ${i}: ${labels[i]} starts with ${label}`));
    await page.waitForLoadState("networkidle");
    const reads = apiReads.length;
    const open = async (group) => {
      await page.locator(`[data-tray-entry="${group}"]`).click();
      await page.locator(".assistant-action-tray").waitFor();
      await noOverflow();
    };
    await open("needs");
    await page.locator('[data-needs-reason="stopped_unfinished"]').waitFor();
    assert.equal(await page.locator("[data-needs-row]").count(), 2, "owner decision and an unattended stopped task only");
    assert.equal(await page.locator('[data-needs-reason="stopped_unfinished"]').count(), 1);
    assert.equal(await page.locator('[data-needs-row="task:unattended"] [data-task-status="unfinished"]').count(), 1);
    assert.equal(await page.locator('[data-needs-row="task:unattended"] [data-task-todo-state]').count(), 1);
    await page.screenshot({ path: name("needs-you") });
    await open("running");
    assert.equal(await page.locator("[data-task-slot]").count(), 2, "working plus an uncounted reported finish");
    assert.equal(await page.locator('[data-task-status="working"]').count(), 1);
    assert.equal(await page.locator('[data-task-status="reported_done"]').textContent(), words[language].done);
    assert.equal(await page.locator('[data-task-mark="done"]').count(), 0, "reported done is not accepted");
    assert.equal(await page.locator("[data-progress-just-finished]").count(), 1);
    assert.equal(await page.locator('[data-task-status="reported_done"]').locator('xpath=ancestor::*[@data-task-slot]').getAttribute("data-counts-open"), "false");
    await page.screenshot({ path: name("acceptance") });
    await page.locator('[data-parked-task-row="parked"]').waitFor();
    assert.equal(await page.locator("[data-parked-task-row]").count(), 2, "explicit park and an aged named wait stay Waiting");
    assert.equal(await page.locator('[data-parked-task-row="named-wait"] [data-task-run-wait]').textContent(), "the release checks finish");
    assert.equal(await page.locator('[data-parked-task-row="named-wait"] [data-task-todo-state]').count(), 1);
    assert.equal(await page.locator('[data-needs-row="task:parked"]').count(), 0);
    assert.equal(await page.locator('[data-task-status="unread"]').count(), 0, "an unread reply never claims completion");
    assert(!(await page.locator(".assistant-action-tray").innerText()).includes("完成未看"));
    await noOverflow();
    await page.screenshot({ path: name("waiting") });
    assert.equal(apiReads.length, reads, "switching groups does not fetch or mutate a to-do");
    assert.deepEqual(errors, []);
    readings.push({ size, theme, language, textSize, entries: 2, needsYou: 2, working: 1, waiting: 2, justFinishedUncounted: 1, recentStoppedOnTodos: 3, groupApiReads: 0 });
    console.log(`PASS ${size} ${theme} ${language} ${textSize}: entries=2, needs_you=2, working=1, waiting=2, just_finished_uncounted=1, group_api_reads=0`);
  } catch (error) {
    await page.screenshot({ path: name("failed") }).catch(() => {});
    throw error;
  } finally { await context.close(); }
}

let browser;
try {
  browser = await chromium.launch({ executablePath: flag("--chromium") || "/usr/bin/chromium", headless: true });
  for (const theme of ["light", "dark"]) for (const size of ["wide", "narrow"]) await run(browser, size, theme, "en");
  for (const language of ["zh-Hans", "ja"]) await run(browser, "narrow", "light", language, "large");
  writeFileSync(join(shots, "readings.json"), JSON.stringify(readings, null, 2) + "\n");
  console.log("PASS task-closeout-groups browser verification");
} catch (error) { console.error(error); process.exitCode = 1; }
finally { clearTimeout(watchdog); await browser?.close(); }
