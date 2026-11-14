// The Machines page and Overview at desktop widths (audit items 3, 25 and 76; docs/web/web.md "Overview" and
// "Machines and settings"). On a hub that pages its Agents list, each computer card counts that computer's agents (the
// hub's own list, read here too), its state and its versions keep a gap between them, and no metric's value is cut
// ("↓ 2.58 M…"); on Overview an extension's lone card is one of the page's two card columns at 1920 px, not the whole
// width, and the whole width at 1280 px. Light and dark screenshots of each.
//
// Needs the web page against the fake hub with the paged list, and Chrome:
//
//   MOCK_SESSIONS_LIST=1 MOCK_MANAGERS=1 PORT=<port> python3 scripts/mock/mock_hub.py &
//   (cd web && PERSONA_HUB_URL=http://127.0.0.1:<port> npx vite --port <web port> --strictPort --host 127.0.0.1 &)
//   node web/e2e/overview-machines-layout.mjs http://127.0.0.1:<web port> <artifact directory> [--channel chrome]
//
// The overview card's place, list and view are answered in the browser (an example extension's "Today's usage": three
// figures and a seven-day bar chart), with the UI-slot features added to the hub's hello, so the run needs no extension
// set up on the fake hub. Nothing is written to the hub. Prints one line per check and a verdict; exits 1 on a failed
// check and 2 on its three-minute timeout.
import { mkdirSync } from "node:fs";
import { join } from "node:path";
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
  console.error("usage: node web/e2e/overview-machines-layout.mjs <web URL> <artifact directory> [--channel chrome]");
  process.exit(2);
}
const origin = new URL(base);
if (!["127.0.0.1", "localhost", "[::1]"].includes(origin.hostname)) {
  console.error("a loopback web server backed by the fake hub is required");
  process.exit(2);
}
mkdirSync(out, { recursive: true });
const watchdog = setTimeout(() => { console.log("VERDICT TIMEOUT overview and machines layout"); process.exit(2); }, 180_000);

const failures = [];
const check = (ok, what, detail = "") => {
  console.log(`${ok ? "ok  " : "FAIL"} ${what}${detail ? `: ${detail}` : ""}`);
  if (!ok) failures.push(what);
};
const hubGet = async (path) => (await fetch(`${base}/api/v1${path}`, { headers: { Authorization: "Bearer mock" } })).json();

// What the hub says each online computer runs: its agents that are not offline, without the assistant's conversations
// and without the child sessions folded under another (roots=1), every page of the complete list.
const expected = new Map();
for (let cursor = ""; ;) {
  const page = await hubGet(`/sessions?summary=1&limit=200&roots=1${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}`);
  for (const s of page.sessions ?? []) {
    if (s.assistant || s.state === "offline") continue;
    expected.set(s.machine_id, (expected.get(s.machine_id) ?? 0) + 1);
  }
  if (!page.has_more) break;
  cursor = page.next_cursor;
}
const machines = (await hubGet("/machines")).machines ?? [];
check([...expected.values()].some((n) => n > 0), "the fake hub has agents on a computer", JSON.stringify(Object.fromEntries(expected)));

const DAYS = ["10-29", "10-30", "10-31", "11-01", "11-02", "11-03", "11-04"];
const VALUES = [2.2, 3.1, 1.1, 0.7, 4.0, 3.5, 1.5];
const TODAY = { sections: [{ items: [
  { type: "stat_grid", stats: [{ label: "Today", value: "1.5M", detail: "-56% vs yesterday", tone: "positive" }, { label: "Last 7 days", value: "17.0M" }, { label: "Cost", value: "$36.20" }] },
  { type: "chart", kind: "bar", unit: "M tokens", series: [{ name: "tokens", points: DAYS.map((label, i) => ({ label, value: VALUES[i] })) }] },
] }] };
async function overviewCard(page) {
  await page.routeWebSocket(/\/api\/v1\/ws/, (ws) => {
    const server = ws.connectToServer();
    server.onMessage((m) => {
      try {
        const o = JSON.parse(m);
        if (o.type === "hello") return ws.send(JSON.stringify({ ...o, features: [...new Set([...(o.features ?? []), "extensions", "extension_ui_slots"])] }));
      } catch { /* not JSON: pass it on */ }
      ws.send(m);
    });
    ws.onMessage((m) => server.send(m));
  });
  await page.route((u) => new URL(u).pathname === "/api/v1/extensions/-/ui", (r) => r.fulfill({ contentType: "application/json", body: JSON.stringify({ ui: [{ extension: "example-gateway", extension_name: "Example gateway", id: "today", title: "Today's usage", placement: "overview_card", size: "compact" }] }) }));
  await page.route((u) => new URL(u).pathname === "/api/v1/extensions/example-gateway/-/ui/today", (r) => r.fulfill({ contentType: "application/json", body: JSON.stringify({ view: TODAY }) }));
}

const browser = await chromium.launch({ channel, headless: true });
async function open(theme, width, height, route, setup) {
  const context = await browser.newContext({ viewport: { width, height } });
  await context.addInitScript((t) => { localStorage.setItem("persona.theme", t); localStorage.setItem("persona.language", "en"); }, theme);
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", (e) => errors.push(String(e)));
  // sign in with the link's token first, then open the page (lessons: wait for the sidebar before navigating)
  await page.goto(`${base}/settings?token=mock`);
  await page.waitForSelector("aside", { timeout: 20_000 });
  if (setup) await setup(page);
  await page.goto(`${base}${route}`);
  return { context, page, errors };
}

try {
  for (const theme of ["light", "dark"]) {
    for (const [width, height] of [[768, 1024], [1024, 768], [1280, 820], [1920, 1080]]) {
      const { context, page, errors } = await open(theme, width, height, "/machines");
      await page.waitForSelector('[data-testid="machine-metrics-summary"]', { timeout: 20_000 });
      await page.waitForFunction(() => document.querySelectorAll('[data-agents="loading"]').length === 0, null, { timeout: 20_000 });
      await page.waitForTimeout(500);
      const cards = await page.$$eval("section", (sections) => sections.filter((s) => s.querySelector("[data-testid=machine-metrics-summary]")).map((s) => {
        const footer = s.lastElementChild;
        const [state, right] = [...footer.children];
        const versions = right?.firstElementChild;
        const shown = versions && getComputedStyle(versions).display !== "none";
        // the text's own width against its box: scrollWidth rounds, and Chrome puts in "…" for half a pixel too much
        const values = [...s.querySelectorAll("[data-testid=machine-metrics-summary] > div > div:nth-child(2)")].map((v) => {
          const range = document.createRange();
          range.selectNodeContents(v);
          return { text: v.textContent, cut: range.getBoundingClientRect().width > v.getBoundingClientRect().width + 0.01 };
        });
        return {
          name: s.querySelector("h2")?.textContent ?? "",
          agents: state.getAttribute("data-agents"),
          footer: state.textContent,
          gap: shown ? versions.getBoundingClientRect().left - state.getBoundingClientRect().right : null,
          values,
        };
      }));
      check(cards.length > 0, `${theme} ${width}: online computer cards are drawn`, String(cards.length));
      for (const c of cards) {
        const id = machines.find((m) => m.name === c.name)?.id;
        const n = expected.get(id) ?? 0;
        check(c.agents === "yes" && (n ? c.footer.startsWith(`${n} agent`) : c.footer === "No agents running"), `${theme} ${width}: ${c.name} counts its ${n} agents`, c.footer);
        if (c.gap !== null) check(c.gap >= 8, `${theme} ${width}: ${c.name}'s state and versions keep a gap`, `${c.gap.toFixed(1)} px`);
        const cut = c.values.filter((v) => v.cut).map((v) => v.text);
        check(cut.length === 0, `${theme} ${width}: ${c.name} shows every value whole`, cut.join(", ") || c.values.map((v) => v.text).join(" | "));
      }
      check(errors.length === 0, `${theme} ${width}: no page errors on Machines`, errors.join("; "));
      await page.screenshot({ path: join(out, `machines-${width}-${theme}.png`) });
      await context.close();
    }
    for (const [width, height, lone] of [[1280, 820, false], [1920, 1080, true]]) {
      const { context, page, errors } = await open(theme, width, height, "/overview", overviewCard);
      const card = await page.waitForSelector("[data-extension-card]", { timeout: 20_000 });
      await page.waitForSelector("[data-extension-card] svg", { timeout: 20_000 });
      const { cardWidth, pageWidth } = await card.evaluate((el) => ({ cardWidth: el.getBoundingClientRect().width, pageWidth: el.parentElement.getBoundingClientRect().width })); // its row spans the page's column
      const share = cardWidth / pageWidth;
      check(lone ? share < 0.55 : share > 0.95, `${theme} ${width}: the overview card is ${lone ? "one of two columns" : "the page's width"}`, `${Math.round(cardWidth)} of ${Math.round(pageWidth)} px`);
      check(errors.length === 0, `${theme} ${width}: no page errors on Overview`, errors.join("; "));
      await page.screenshot({ path: join(out, `overview-${width}-${theme}.png`) });
      await context.close();
    }
  }
} catch (e) {
  check(false, "the run finished", String(e));
} finally {
  await browser.close();
  clearTimeout(watchdog);
}
console.log(failures.length ? `VERDICT FAIL overview and machines layout: ${failures.length} failed` : "VERDICT PASS overview and machines layout");
process.exit(failures.length ? 1 : 0);
