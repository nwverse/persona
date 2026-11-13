// Production-browser acceptance against the real hub routes in a disposable source copy.
// Usage: node web/e2e/agents-performance.mjs <output-directory> [port]
import { spawn } from "node:child_process";
import { cp, copyFile, mkdir, mkdtemp, readFile, writeFile } from "node:fs/promises";
import { dirname, resolve, join } from "node:path";
import { fileURLToPath } from "node:url";
import assert from "node:assert/strict";
import { chromium } from "playwright-core";

const web = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const output = resolve(process.argv[2] ?? "agents-performance-results");
const port = Number(process.argv[3] ?? 18983);
await mkdir(output, { recursive: true });
const scratch = await mkdtemp(join(output, "server-copy-"));
await cp(join(web, "../server"), join(scratch, "server"), { recursive: true });
await cp(join(web, "../sdk/go"), join(scratch, "sdk/go"), { recursive: true });
await copyFile(join(web, "e2e/agents_hub_test.go"), join(scratch, "server/internal/hub/web_agents_fixture_test.go"));
const env = { ...process.env, WEB_AGENTS_DIST: join(web, "dist"), WEB_AGENTS_PORT: String(port), GOTMPDIR: scratch };
delete env.PERSONA_TEST_DATABASE_URL;
delete env.PERSONA_TEST_DATABASE_URL;

async function command(program, args, cwd, timeout) {
  const child = spawn(program, args, { cwd, env, stdio: ["ignore", "pipe", "pipe"] });
  let text = "";
  child.stdout.on("data", (data) => { text += data; });
  child.stderr.on("data", (data) => { text += data; });
  const timer = setTimeout(() => child.kill("SIGTERM"), timeout);
  const code = await new Promise((done) => child.on("exit", done));
  clearTimeout(timer);
  if (code !== 0) throw new Error(`${program} failed (${code}): ${text}`);
  return text;
}
await command("go", ["test", "-c", "-o", join(scratch, "hub.test"), "./internal/hub"], join(scratch, "server"), 180_000);
const fixture = spawn(join(scratch, "hub.test"), ["-test.run=^TestWebAgentsFixture$", "-test.timeout=16m", "-test.v"], { env, cwd: scratch, stdio: ["ignore", "pipe", "pipe"] });
let log = "";
fixture.stdout.on("data", (data) => { log += data; });
fixture.stderr.on("data", (data) => { log += data; });
let browser;
const deadline = setTimeout(() => {
  console.error("WEB_AGENTS_PERFORMANCE TIMEOUT");
  fixture.kill("SIGTERM");
  void browser?.close();
  process.exit(1);
}, 14 * 60_000);
const url = `http://127.0.0.1:${port}`;
try {
  const started = Date.now();
  while (!log.includes("WEB_AGENTS_READY")) {
    if (fixture.exitCode !== null || Date.now() - started > 30_000) throw new Error(`fixture not ready: ${log}`);
    await new Promise((done) => setTimeout(done, 100));
  }
  browser = await chromium.launch({ channel: "chrome", headless: true, args: ["--no-sandbox"] });
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: "en-US", colorScheme: "light" });
  await context.addInitScript(() => {
    localStorage.setItem("persona.token", "synthetic-browser-token");
    localStorage.setItem("persona.language", "en");
    const observer = new MutationObserver(() => {
      if (document.querySelector("main [data-session-row]")) {
        observer.disconnect();
        requestAnimationFrame(() => requestAnimationFrame(() => { window.__agentsFirstPaint = performance.now(); }));
      }
    });
    observer.observe(document, { childList: true, subtree: true });
  });
  const page = await context.newPage();
  const errors = [];
  const requests = [];
  const notices = [];
  page.on("pageerror", (error) => errors.push(error.message));
  page.on("websocket", (socket) => socket.on("framereceived", (frame) => {
    try { const event = JSON.parse(String(frame.payload)); if (["sessions", "sessions_delta", "sessions_list"].includes(event.type)) notices.push({ type: event.type, bytes: Buffer.byteLength(String(frame.payload)) }); } catch { /* non-JSON control frame */ }
  }));
  page.on("response", async (res) => {
    if (new URL(res.url()).pathname !== "/api/v1/sessions") return;
    const body = await res.body();
    const data = JSON.parse(body.toString());
    requests.push({ query: new URL(res.url()).search, bytes: body.length, rows: data.sessions?.length ?? 0, total: data.total });
  });
  const cdp = await context.newCDPSession(page);
  await page.goto(`${url}/agents`);
  const main = page.locator("main [data-paged-sessions=runtime]");
  await main.locator("[data-session-row]").first().waitFor();
  await page.waitForTimeout(700);
  const firstPaint = await page.evaluate(() => {
    const resource = performance.getEntriesByType("resource").find((r) => {
      const url = new URL(r.name);
      return url.pathname === "/api/v1/sessions" && url.searchParams.get("limit") === "50" && url.searchParams.get("agents_only") === "1";
    });
    return { navigationToRowsMs: window.__agentsFirstPaint, responseToRowsMs: window.__agentsFirstPaint - resource?.responseEnd, firstResponseMs: resource?.responseEnd, firstRequestMs: resource?.startTime };
  });
  const initialPages = requests.filter((r) => r.query.includes("limit=50") && !r.query.includes("since=") && !r.query.includes("cursor="));
  const first = initialPages.filter((r) => r.query.includes("agents_only=1"));
  assert.equal(initialPages.length - first.length, 1, "exactly one initial all-sessions page");
  assert.equal(first.length, 1, "exactly one initial Agents page");
  assert.equal(first[0].rows, 50);
  assert.equal(first[0].total, 8000);
  assert.ok(!requests.some((r) => !r.query), "no legacy full-list GET");
  assert.ok(notices.length > 0 && notices.every((n) => n.type === "sessions_list" && n.bytes < 160));
  assert.equal(await main.locator("[data-session-row]").count() < 50, true);
  await page.screenshot({ path: join(output, "agents-light.png"), fullPage: false });
  await page.emulateMedia({ colorScheme: "dark" });
  await page.evaluate(() => document.documentElement.classList.add("dark"));
  await page.screenshot({ path: join(output, "agents-dark.png"), fullPage: false });

  // A title-only update without updated_at changing must reach the loaded row via since, not a fresh baseline.
  const beforeChange = requests.length;
  await fetch(`${url}/__fixture/change?row=1999&title=Incremental%20demo%20rename`);
  await main.getByText("Incremental demo rename", { exact: true }).waitFor();
  assert.ok(requests.slice(beforeChange).some((r) => r.query.includes("agents_only=1") && r.query.includes("since=") && r.rows === 1));
  assert.ok(!requests.slice(beforeChange).some((r) => !r.query.includes("since=") && r.query.includes("limit=")));

  await cdp.send("Tracing.start", { categories: "devtools.timeline,blink.user_timing,disabled-by-default-devtools.timeline.frame", transferMode: "ReturnAsStream" });
  const frames = await main.evaluate(async (root) => {
    const scroller = (() => { for (let p = root.parentElement; p; p = p.parentElement) if (/auto|scroll/.test(getComputedStyle(p).overflowY)) return p; })();
    if (!scroller) throw new Error("no session scroller");
    const samples = [];
    let maxRows = 0;
    let last = 0;
    const start = performance.now();
    await new Promise((done) => {
      function tick(now) {
        if (last) samples.push(now - last);
        last = now;
        maxRows = Math.max(maxRows, root.querySelectorAll("[data-session-row]").length);
        scroller.scrollTop = Math.min(scroller.scrollTop + 240, scroller.scrollHeight - scroller.clientHeight);
        const finished = root.dataset.hasMore === "false" && scroller.scrollTop + scroller.clientHeight >= scroller.scrollHeight - 1;
        if (now - start < 45_000 && !finished) requestAnimationFrame(tick); else done();
      }
      requestAnimationFrame(tick);
    });
    const sorted = [...samples].sort((a, b) => a - b);
    return { elapsedMs: performance.now() - start, frameCount: samples.length, fps: samples.length * 1000 / samples.reduce((a, b) => a + b, 0), p95FrameMs: sorted[Math.floor(sorted.length * .95)], over33ms: samples.filter((x) => x > 33.4).length, maxDOMRows: maxRows };
  });
  const completion = new Promise((done) => cdp.once("Tracing.tracingComplete", done));
  await cdp.send("Tracing.end");
  const { stream } = await completion;
  let trace = "";
  for (;;) { const chunk = await cdp.send("IO.read", { handle: stream }); trace += chunk.data; if (chunk.eof) break; }
  await cdp.send("IO.close", { handle: stream });
  await writeFile(join(output, "agents-scroll.trace.json"), trace);
  await page.waitForTimeout(300);
  assert.ok(frames.maxDOMRows <= 60, `virtual list DOM grew to ${frames.maxDOMRows}`);
  assert.equal(await main.getAttribute("data-loaded-sessions"), "8000", "cursor traversal loads every session");
  assert.equal(await main.getAttribute("data-has-more"), "false");
  const traversalPages = requests.filter((r) => r.query.includes("cursor=")).length;
  assert.ok(requests.filter((r) => r.query.includes("cursor=")).every((r) => r.rows <= 50));
  assert.equal(await page.locator("vite-error-overlay").count(), 0);
  assert.deepEqual(errors, []);

  // Server filters and aggregate counts are independently verified, including a scoped space.
  await main.getByRole("radio", { name: /Working/ }).click();
  await page.waitForTimeout(700);
  assert.ok(requests.some((r) => r.query.includes("state=working") && r.total === 2000 && r.rows === 50));
  await main.getByRole("radio", { name: "All", exact: true }).click();
  await main.getByPlaceholder("Search sessions").fill("Demo task 1234");
  await page.waitForTimeout(700);
  assert.ok(requests.some((r) => r.query.includes("q=Demo+task+1234") && r.rows === 4 && r.total === 4));
  await main.getByPlaceholder("Search sessions").fill("");
  await page.waitForTimeout(400);
  await main.getByLabel("Browse by").selectOption("group");
  await main.getByLabel("Computer", { exact: true }).selectOption("demo-0");
  await main.getByLabel("Filter group").fill("space-04");
  await page.waitForTimeout(700);
  assert.ok(requests.some((r) => r.query.includes("group=space-04") && r.query.includes("machine_id=demo-0") && r.total === 200 && r.rows === 50));
  await main.getByLabel("Browse by").selectOption("project");
  await main.getByLabel("Filter group").fill("/demo/project-04");
  await page.waitForTimeout(700);
  assert.ok(requests.some((r) => r.query.includes("project=%2Fdemo%2Fproject-04") && r.total === 400 && r.rows === 50));
  assert.equal(traversalPages, 159);
  // A cold deep link outside the loaded first page reads only its exact full record, never a list scan.
  const snapshotContext = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: "en-US" });
  await snapshotContext.addInitScript(() => { localStorage.setItem("persona.token", "synthetic-browser-token"); localStorage.setItem("persona.language", "en"); });
  const snapshotPage = await snapshotContext.newPage();
  const snapshotRequests = [];
  snapshotPage.on("response", async (response) => {
    const address = new URL(response.url());
    if (address.pathname === "/api/v1/sessions") snapshotRequests.push({ params: Object.fromEntries(address.searchParams), data: await response.json() });
  });
  await snapshotPage.goto(`${url}/s/demo-0/session-0000`);
  await snapshotPage.getByText("Oldest demo queued instruction", { exact: true }).waitFor();
  await snapshotPage.getByText("Synthetic chat transcript", { exact: true }).waitFor();
  await snapshotPage.screenshot({ path: join(output, "chat-light.png") });
  await snapshotPage.evaluate(() => document.documentElement.classList.add("dark"));
  await snapshotPage.screenshot({ path: join(output, "chat-dark.png") });
  const exactFirst = snapshotRequests.filter((request) => request.params.session_id);
  assert.equal(exactFirst.length, 1);
  assert.equal(exactFirst[0].params.summary, "0");
  assert.equal(exactFirst[0].params.machine_id, "demo-0");
  assert.equal(exactFirst[0].params.session_id, "session-0000");
  assert.equal(exactFirst[0].data.sessions.length, 1);
  assert.ok(exactFirst[0].data.sessions[0].background.length > 0);
  assert.deepEqual(exactFirst[0].data.sessions[0].queued, ["Oldest demo queued instruction"]);
  const longTitle = "Exact full title " + "complete ".repeat(34);
  await fetch(`${url}/__fixture/change?row=0&clear_tasks=1&title=${encodeURIComponent(longTitle)}`);
  await snapshotPage.locator("header").getByText(longTitle, { exact: true }).waitFor();
  await snapshotPage.getByText("Oldest demo queued instruction", { exact: true }).waitFor({ state: "detached" });
  assert.ok(snapshotRequests.filter((request) => request.params.session_id).length >= 2);
  assert.ok(snapshotRequests.every((request) => Object.keys(request.params).length > 0));
  assert.ok(!snapshotRequests.some((request) => request.params.cursor));
  await snapshotContext.close();

  // A hub with pages but no lookup feature retains complete bounded rows and uses its own deltas for chat recovery.
  const fallbackContext = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: "en-US" });
  await fallbackContext.addInitScript(() => { localStorage.setItem("persona.token", "synthetic-browser-token"); localStorage.setItem("persona.language", "en"); });
  const fallbackPage = await fallbackContext.newPage();
  const fallbackRequests = [];
  fallbackPage.on("response", async (response) => { const address = new URL(response.url()); if (address.pathname === "/api/v1/sessions") fallbackRequests.push(Object.fromEntries(address.searchParams)); });
  await fallbackPage.routeWebSocket(/\/api\/v1\/ws/, (socket) => {
    const upstream = socket.connectToServer();
    upstream.onMessage((message) => { const event = JSON.parse(String(message)); if (event.type === "hello") event.features = event.features.filter((feature) => feature !== "sessions_lookup"); socket.send(JSON.stringify(event)); });
    socket.onMessage((message) => upstream.send(message));
  });
  await fallbackPage.goto(`${url}/agents`);
  await fallbackPage.locator('main a[href="/s/demo-0/session-1999"]').click();
  await fallbackPage.getByText("Newest demo queued instruction", { exact: true }).waitFor();
  await fetch(`${url}/__fixture/change?row=1999&clear_tasks=1&title=Legacy%20paged%20chat%20recovered`);
  await fallbackPage.locator("header").getByText("Legacy paged chat recovered", { exact: true }).waitFor();
  await fallbackPage.getByText("Newest demo queued instruction", { exact: true }).waitFor({ state: "detached" });
  assert.ok(fallbackRequests.some((params) => params.summary === "0" && params.limit === "50"));
  assert.ok(fallbackRequests.some((params) => params.since && params.summary === "0"));
  assert.ok(fallbackRequests.every((params) => !params.session_id && Object.keys(params).length > 0));
  await fallbackContext.close();

  const personalContext = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: "en-US" });
  await personalContext.addInitScript(() => { localStorage.setItem("persona.token", "synthetic-browser-token"); localStorage.setItem("persona.language", "en"); });
  const personalPage = await personalContext.newPage();
  const personalRequests = [];
  let personalChanged = false;
  const personalRows = Array.from({ length: 8000 }, (_, i) => ({ id: `demo:s${i}`, source: "demo", sessionKey: `s${i}`, accountId: "demo-account", provider: "demo-provider", title: `Personal demo ${String(i).padStart(4, "0")}`, canSend: true, unreadCount: i % 2, rankTimeMs: 8000 - i }));
  await personalPage.route("**/api/v1/agents/sessions*", async (route) => {
    const params = new URL(route.request().url()).searchParams;
    personalRequests.push(Object.fromEntries(params));
    const selected = personalRows.filter((row) => (!params.get("state") || (row.unreadCount ? "unread" : "idle") === params.get("state")) && (!params.get("q") || row.title.toLowerCase().includes(params.get("q").toLowerCase())));
    const counts = { total: selected.length, machine: {}, project: {}, group: { "demo-account": selected.length }, state: { unread: selected.filter((row) => row.unreadCount).length, idle: selected.filter((row) => !row.unreadCount).length }, revision: personalChanged ? "p2" : "p1" };
    if (params.get("counts") === "1") { await route.fulfill({ json: counts }); return; }
    const offset = Number(params.get("cursor") ?? 0);
    const rows = params.has("since") ? (personalChanged ? [personalRows[0]] : []) : selected.slice(offset, offset + 50);
    const more = !params.has("since") && offset + 50 < selected.length;
    await route.fulfill({ json: { sessions: rows, remove: [], total: selected.length, revision: counts.revision, has_more: more, ...(more ? { next_cursor: String(offset + 50) } : {}) } });
  });
  await personalPage.goto(`${url}/a`);
  const personalMain = personalPage.locator("main [data-paged-sessions=personal]");
  await personalMain.locator("[data-session-row]").first().waitFor();
  await personalPage.waitForTimeout(300);
  assert.equal(await personalMain.getAttribute("data-loaded-sessions"), "50");
  assert.equal(personalRequests.filter((r) => r.limit === "50" && !r.cursor && !r.since).length, 1);
  assert.ok(personalRequests.every((r) => r.counts === "1" || r.summary === "1"), "no premature personal-agent full list");
  await personalPage.screenshot({ path: join(output, "personal-light.png") });
  await personalPage.evaluate(() => document.documentElement.classList.add("dark"));
  await personalPage.screenshot({ path: join(output, "personal-dark.png") });
  await personalMain.evaluate((root) => { for (let p = root.parentElement; p; p = p.parentElement) if (/auto|scroll/.test(getComputedStyle(p).overflowY)) { p.scrollTop = p.scrollHeight; break; } });
  await personalPage.waitForFunction(() => Number(document.querySelector("main [data-paged-sessions=personal]")?.dataset.loadedSessions) >= 100);
  assert.ok(personalRequests.some((r) => r.cursor === "50"));
  assert.ok(await personalMain.locator("[data-session-row]").count() <= 60);
  await personalMain.evaluate((root) => { for (let p = root.parentElement; p; p = p.parentElement) if (/auto|scroll/.test(getComputedStyle(p).overflowY)) { p.scrollTop = 0; break; } });
  personalRows[0].title = "Incremental personal rename";
  personalChanged = true;
  await fetch(`${url}/__fixture/agents`);
  await personalMain.getByText("Incremental personal rename", { exact: true }).waitFor();
  assert.ok(personalRequests.some((r) => r.since === "p1"));
  await personalContext.close();
  const legacyContext = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: "en-US" });
  await legacyContext.addInitScript(() => { localStorage.setItem("persona.token", "synthetic-browser-token"); localStorage.setItem("persona.language", "en"); });
  const legacyPage = await legacyContext.newPage();
  let legacyRows = 0;
  legacyPage.on("response", async (response) => {
    const address = new URL(response.url());
    if (address.pathname === "/api/v1/sessions" && !address.search) legacyRows = (await response.json()).sessions.length;
  });
  await legacyPage.routeWebSocket(/\/api\/v1\/ws/, (socket) => {
    const upstream = socket.connectToServer();
    upstream.onMessage((message) => {
      const event = JSON.parse(String(message));
      if (event.type === "sessions_list") return;
      if (event.type === "hello") event.features = event.features.filter((feature) => feature !== "sessions_list" && feature !== "sessions_lookup");
      socket.send(JSON.stringify(event));
    });
    socket.onMessage((message) => upstream.send(message));
  });
  await legacyPage.goto(`${url}/agents`);
  await legacyPage.getByText("Demo task 1998", { exact: true }).first().waitFor();
  assert.equal(legacyRows, 8000);
  assert.equal(await legacyPage.locator("[data-paged-sessions]").count(), 0);
  await legacyContext.close();
  await writeFile(join(output, "results.json"), JSON.stringify({ fixture: "real hub routes, 8000 synthetic sessions, no real node or production hub", viewport: "1440x900", browser: browser.version(), firstPage: first[0], timing: firstPaint, scrolling: frames, allRowsLoaded: 8000, traversalPages, legacyRows, snapshotRequests, fallbackRequests, personalRequests, cursorRequests: requests.filter((r) => r.query.includes("cursor=")).length, notices, requests, errors }, null, 2));
  console.log(JSON.stringify({ firstPage: first[0], timing: firstPaint, scrolling: frames, allRowsLoaded: 8000, traversalPages, legacyRows, snapshotRequests, fallbackRequests, personalRequests, cursorRequests: requests.filter((r) => r.query.includes("cursor=")).length }, null, 2));
  console.log("WEB_AGENTS_PERFORMANCE PASS");
} catch (error) {
  console.error("WEB_AGENTS_PERFORMANCE FAIL", error);
  process.exitCode = 1;
} finally {
  clearTimeout(deadline);
  await browser?.close();
  await fetch(`${url}/__fixture/stop`).catch(() => {});
  fixture.kill("SIGTERM");
  await writeFile(join(output, "hub.log"), log);
}
