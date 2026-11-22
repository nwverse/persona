// How often the web app reads GET /api/v1/sessions while it sits open (#3759, docs/operations/testing.md "Session list reads"):
// one quiet minute, then one minute while the fake hub's working agents change about twice a second (a list hint
// each time; --rows of them a round, default one), counted by the fake hub per kind of read (full list, complete baseline pages, `since` deltas, counts).
//
// Needs the web page against the fake hub in paging mode with the long list, and Chrome or Edge:
//
//   MOCK_SESSIONS_LIST=1 MOCK_AGENTS=2 PORT=18995 python3 scripts/mock/mock_hub.py &
//   (cd web && PERSONA_HUB_URL=http://127.0.0.1:18995 npx vite --port 5995 --strictPort &)
//   node web/e2e/session-list-reads.mjs http://127.0.0.1:5995 --hub http://127.0.0.1:18995 [--seconds 60] [--rows 1] [--channel msedge] [--max-full 0]
//
// The fake hub's default pins keep the sidebar on its complete-list fallback. Prints the counts as JSON and a verdict:
// fails when the churn minute has more than --max-full complete reads (no-query lists plus baseline page runs).
import { chromium } from "playwright-core";

const args = process.argv.slice(2);
const flag = (name, fallback) => {
  const i = args.indexOf(name);
  return i >= 0 ? args[i + 1] : fallback;
};
const base = (args[0] ?? "").replace(/\/$/, "");
const hub = (flag("--hub") ?? "").replace(/\/$/, "");
const seconds = Number(flag("--seconds", "60"));
const channel = flag("--channel", "msedge");
const maxFull = flag("--max-full");
// How many working agents change each half second: a real hub changes a row or two a second; 0 is every working one.
const rows = Number(flag("--rows", "1"));
if (!base || !hub) {
  console.error("usage: node web/e2e/session-list-reads.mjs <web URL> --hub <fake hub URL> [--seconds 60] [--rows 1] [--channel msedge] [--max-full N]");
  process.exit(2);
}
const get = async (path) => (await fetch(`${hub}${path}`, { signal: AbortSignal.timeout(10_000) })).json();
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const web = (reads) => reads.reads?.web ?? {};
const sum = (rows, key) => Object.values(rows).reduce((total, row) => total + row[key], 0);
const summary = (reads) => {
  const rows = web(reads);
  return { seconds: reads.seconds, hints: reads.hints, requests: sum(rows, "count"), bytes: sum(rows, "bytes"), by_kind: rows };
};

const browser = await chromium.launch({ channel, headless: true });
try {
  const page = await (await browser.newContext({ viewport: { width: 1280, height: 900 } })).newPage();
  await page.goto(`${base}/?token=mock`);
  await page.waitForFunction(() => localStorage.getItem("persona.token"), null, { timeout: 15_000 });
  await page.reload();
  await page.waitForSelector("main", { timeout: 15_000 });
  await sleep(5_000); // first load, counts and the sidebar's fallback settle
  await get("/mock/sessions_reads?reset=1");
  await sleep(seconds * 1000);
  const quiet = summary(await get("/mock/sessions_reads?reset=1"));
  const rounds = Math.round(seconds * 2);
  await get(`/mock/churn?n=${rounds}&ms=500&rows=${rows}`);
  await sleep(seconds * 1000 + 1_500);
  const busy = summary(await get("/mock/sessions_reads?reset=1"));
  const full = (s) => (s.by_kind.full?.count ?? 0);
  console.log(JSON.stringify({ quiet, busy }, null, 1));
  const verdict = maxFull === undefined || full(busy) <= Number(maxFull) ? "PASS" : "FAIL";
  console.log(`VERDICT ${verdict} quiet: ${quiet.requests} reads / ${quiet.bytes} B, full ${full(quiet)}; busy: ${busy.hints} hints, ${busy.requests} reads / ${busy.bytes} B, full ${full(busy)}`);
  process.exitCode = verdict === "PASS" ? 0 : 1;
} finally {
  await browser.close();
}
