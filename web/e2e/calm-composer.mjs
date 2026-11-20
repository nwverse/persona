// Verify the calm Assistant composer with synthetic HTTP/WebSocket data only (#7770).
// node web/e2e/calm-composer.mjs <origin> <artifacts> [--chromium <executable>]
import assert from "node:assert/strict";
import { mkdir, writeFile } from "node:fs/promises";
import { resolve } from "node:path";
import { chromium } from "playwright-core";

const argv = process.argv.slice(2);
const [address, artifacts, ...flags] = argv;
assert.ok(address && artifacts, "usage: calm-composer.mjs <origin> <artifacts> [--chromium <executable>]");
const chromiumPath = flags.indexOf("--chromium") >= 0 ? flags[flags.indexOf("--chromium") + 1] : undefined;
const base = new URL(address);
const output = resolve(artifacts);
await mkdir(output, { recursive: true });
const AT = "2026-01-01T10:00:00Z";
const MAIN_ID = "demo-main";
const OTHER_ID = "demo-other";
const MACHINE = { id: "demo", name: "Computer A", online: true, os: "linux" };
const mainSession = {
  machine_id: "demo", session_id: MAIN_ID, title: "Persona", state: "working", status: "working", cwd: "/demo",
  updated_at: AT, assistant: true, started_by_persona: true, capabilities: { send: true, approve: true, interrupt: true },
  queued: ["Owner first", "Owner second", "Relayed from supervisor"], queued_relayed: 1,
};
const otherSession = {
  machine_id: "demo", session_id: OTHER_ID, title: "Other synthetic chat", state: "needs_you", status: "needs_you",
  pending_interactions: 1, cwd: "/demo", updated_at: AT, agent: "claude",
};
const ownCard = {
  id: "demo-own-permission", machine_id: "demo", session_id: MAIN_ID, session_title: "Persona", kind: "permission",
  tool_name: "Bash", tool_input: { command: "npm test", description: "Run synthetic checks" }, created_at: AT,
};
const otherCard = {
  id: "demo-other-question", machine_id: "demo", session_id: OTHER_ID, session_title: otherSession.title, kind: "question",
  questions: [{ question: "Which synthetic option?", options: [{ label: "One" }, { label: "Two" }] }], created_at: AT,
};
const items = Array.from({ length: 36 }, (_, i) => ({
  id: `demo-item-${i}`, seq: i + 1, role: i % 2 ? "assistant" : "user", kind: "text", ts: AT,
  text: `${i % 2 ? "Assistant" : "User"} synthetic paragraph ${i + 1}. This line gives the chat enough scrollable history to test reading stability.`,
}));
const result = { status: "running", runs: [] };
const browser = await chromium.launch({ executablePath: chromiumPath, headless: true, args: ["--no-sandbox"] });
const deadline = setTimeout(() => {
  result.status = "timeout";
  console.log("TIMEOUT calm composer");
  void writeFile(`${output}/results.json`, JSON.stringify(result, null, 2)).finally(() => process.exit(1));
}, 240_000);

const frames = (page) => page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))));
const gap = (page) => page.evaluate(() => {
  const s = document.querySelector("[data-chat-scroll]");
  return s ? s.scrollHeight - s.scrollTop - s.clientHeight : Number.POSITIVE_INFINITY;
});
const atEnd = async (page, what) => {
  await page.waitForFunction(() => {
    const s = document.querySelector("[data-chat-scroll]");
    return s && s.scrollHeight - s.scrollTop - s.clientHeight <= 2;
  }, undefined, { timeout: 20_000 });
  assert.ok(await gap(page) <= 2, `${what}: not at the newest end`);
};

const detailsHeight = (page) => page.locator(".queue-collapse").evaluate((el) => el.getBoundingClientRect().height);
const waitDetails = async (page, open) => {
  await page.waitForFunction((wanted) => {
    const el = document.querySelector(".queue-collapse");
    return el && (wanted ? el.getBoundingClientRect().height > 0 : el.getBoundingClientRect().height === 0);
  }, open);
};

try {
  for (const width of [390, 1280]) for (const theme of ["light", "dark"]) {
    const name = `${width}-${theme}`;
    const context = await browser.newContext({ viewport: { width, height: 900 }, locale: "en-US", colorScheme: theme, reducedMotion: "reduce", isMobile: width < 500, hasTouch: width < 500, serviceWorkers: "block" });
    await context.addInitScript(({ theme }) => {
      localStorage.setItem("persona.token", "demo");
      localStorage.setItem("persona.language", "en");
      localStorage.setItem("persona.theme", theme);
    }, { theme });
    let remaining = [ownCard, otherCard];
    let queued = [...mainSession.queued];
    let relayed = mainSession.queued_relayed;
    let socket;
    const writes = [];
    await context.route("**/api/v1/**", (route) => {
      const request = route.request();
      const path = new URL(request.url()).pathname.replace("/api/v1", "");
      if (request.method() !== "GET") {
        writes.push({ method: request.method(), path });
        if (path === `/interactions/${ownCard.id}/resolve`) remaining = remaining.filter((ix) => ix.id !== ownCard.id);
        return route.fulfill({ status: 200, contentType: "application/json", body: "{}" });
      }
      const session = { ...mainSession, queued, queued_relayed: relayed };
      const data = path === "/machines" ? { machines: [MACHINE] }
        : path === "/sessions" ? { sessions: [session, otherSession] }
        : path === "/interactions" ? { interactions: remaining }
        : path === "/assistant" ? { machine_id: "demo", main: MAIN_ID, conversations: [session, otherSession], tasks: [], memory: [] }
        : path === "/todos" ? { todos: [] }
        : path.endsWith("/feed") ? { items: [], plans: {}, task_cards: [] }
        : path.endsWith("/items") ? { items, has_more: false }
        : path === `/sessions/demo/${MAIN_ID}` ? { session }
        : path === `/sessions/demo/${OTHER_ID}` ? { session: otherSession }
        : {};
      return route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(data) });
    });
    await context.routeWebSocket("**/api/v1/ws**", (ws) => {
      socket = ws;
      ws.send(JSON.stringify({ type: "hello", server_version: "demo", hub_name: "Demo hub", features: [] }));
      ws.onMessage((message) => { if (String(message).includes("app_ping")) ws.send(JSON.stringify({ type: "pong" })); });
    });
    const pushQueue = async (next, relayedCount = 1) => {
      queued = next;
      relayed = relayedCount;
      socket?.send(JSON.stringify({ type: "sessions", machine_id: "demo", sessions: [{ ...mainSession, queued, queued_relayed: relayed }, otherSession] }));
      await frames(page);
    };
    const stream = async (text) => {
      socket?.send(JSON.stringify({ type: "delta", machine_id: "demo", session_id: MAIN_ID, text }));
      await frames(page);
    };
    const page = await context.newPage();
    page.setDefaultTimeout(20_000);
    const errors = [];
    page.on("pageerror", (error) => errors.push(String(error)));
    const run = { name, checks: [], errors };
    result.runs.push(run);
    await page.goto(base.href);
    const summary = page.locator("[data-queue-summary]");
    await summary.waitFor();
    assert.match(await summary.textContent(), /3 queued/);
    const collapsedHeight = (await summary.boundingBox()).height;
    assert.equal(await detailsHeight(page), 0, "fresh queue is not collapsed");
    assert.equal(await page.getByText("Relayed from supervisor", { exact: false }).count(), 0);
    run.checks.push("fresh queue collapsed to one count");

    await pushQueue(["Owner only", "Relayed one", "Relayed two", "Relayed three", "Relayed four"], 4);
    await page.waitForFunction(() => document.querySelector("[data-queue-summary]")?.textContent?.includes("5 queued"));
    assert.ok(Math.abs((await summary.boundingBox()).height - collapsedHeight) < 1, "queue count changed the collapsed height");
    await page.locator("[data-queue-toggle]").click();
    await waitDetails(page, true);
    assert.equal(await page.locator("[data-queue-row]:visible").count(), 1, "explicit disclosure did not show the owner's row");
    assert.equal(await page.getByText("Relayed from supervisor", { exact: false }).count(), 0);
    assert.equal(await page.locator("[data-queue-relayed-summary]").count(), 0);
    await page.locator("[data-queue-toggle]").click();
    await waitDetails(page, false);
    run.checks.push("count-independent summary; owner-only disclosure");

    // Answering this chat's card puts the other chat's request inside the same capsule row.
    await page.getByRole("button", { name: "Allow", exact: true }).click();
    const next = page.locator("[data-assistant-actionbar] [data-chat-next-waiting]");
    await next.waitFor();
    const positions = await page.locator("[data-assistant-actionbar] [data-tray-entry], [data-assistant-actionbar] [data-chat-next-waiting]").evaluateAll((els) => els.map((el) => el.getBoundingClientRect().top));
    assert.ok(Math.max(...positions) - Math.min(...positions) <= 1, `next request is on another row: ${positions}`);
    run.checks.push("status capsules and next request share one row");
    await page.screenshot({ path: `${output}/${name}-collapsed.png` });

    // Streaming hides the whole action row and returns it, while the list follows the newest end.
    const bar = page.locator("[data-assistant-actionbar]");
    await stream("A synthetic partial answer that needs the reading room.");
    await bar.waitFor({ state: "hidden" });
    assert.equal(await next.count(), 0);
    await atEnd(page, `${name} stream start`);
    await page.screenshot({ path: `${output}/${name}-streaming.png` });
    await stream("");
    await bar.waitFor({ state: "visible" });
    await atEnd(page, `${name} stream end`);
    run.checks.push("streaming collapse preserves newest-end following");

    // A reader away from the end keeps the same visible row through disclosure and stream transitions.
    const scroller = page.locator("[data-chat-scroll]");
    await scroller.hover();
    await page.mouse.wheel(0, -10000);
    await page.waitForFunction(() => {
      const s = document.querySelector("[data-chat-scroll]");
      return s && s.scrollHeight - s.scrollTop - s.clientHeight > 400;
    });
    const before = await page.evaluate(() => {
      const s = document.querySelector("[data-chat-scroll]");
      const sr = s.getBoundingClientRect();
      const row = [...s.querySelectorAll("[data-user-bubble]")].find((el) => {
        const r = el.getBoundingClientRect();
        return r.top >= sr.top && r.bottom <= sr.bottom;
      });
      if (!row) return null;
      row.setAttribute("data-calm-reading-row", "");
      return row.getBoundingClientRect().top;
    });
    assert.notEqual(before, null, "no visible historical row to track");
    const readingTop = async () => page.evaluate(() => document.querySelector("[data-calm-reading-row]")?.getBoundingClientRect().top ?? Number.NaN);
    const assertReading = async (what) => {
      await frames(page);
      assert.ok(Math.abs(await readingTop() - before) <= 2, `${what}: historical row moved from ${before} to ${await readingTop()}`);
    };
    await page.locator("[data-queue-toggle]").click();
    await waitDetails(page, true);
    await assertReading(`${name} queue open`);
    await page.locator("[data-queue-toggle]").click();
    await waitDetails(page, false);
    await assertReading(`${name} queue close`);
    await stream("Another partial answer while the reader is above.");
    await bar.waitFor({ state: "hidden" });
    await assertReading(`${name} stream start while reading`);
    await stream("");
    await bar.waitFor({ state: "visible" });
    await assertReading(`${name} stream end while reading`);
    run.checks.push("queue and stream transitions preserve a manual reading row");

    // A relayed-only queue has a count but cannot open an empty detail panel.
    await pushQueue(["Only a relayed synthetic message"]);
    await page.waitForFunction(() => document.querySelector("[data-queue-summary]")?.textContent?.includes("1 queued"));
    assert.equal(await page.locator("[data-queue-toggle]").count(), 0);
    assert.equal(await detailsHeight(page), 0, "relayed-only queue has an expanded panel");
    assert.equal(await page.getByText("Only a relayed synthetic message", { exact: false }).count(), 0);
    run.checks.push("relayed-only queue is informational and cannot expand");
    assert.deepEqual(errors, [], `${name}: browser errors`);
    assert.ok(writes.every((w) => w.path === `/interactions/${ownCard.id}/resolve` || /\/(seen|today)$/.test(w.path)), `unexpected writes: ${JSON.stringify(writes)}`);
    console.log(`PASS ${name}: ${run.checks.join("; ")}`);
    await context.close();
  }
  result.status = "pass";
} catch (error) {
  result.status = result.status === "timeout" ? "timeout" : "fail";
  result.error = String(error?.stack ?? error);
  console.error(result.error);
} finally {
  clearTimeout(deadline);
  await writeFile(`${output}/results.json`, JSON.stringify(result, null, 2));
  await browser.close().catch(() => {});
  console.log(`${result.status.toUpperCase()} calm composer`);
  process.exit(result.status === "pass" ? 0 : 1);
}
