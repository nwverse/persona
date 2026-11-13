// The paged Agents list drawn by group (#6348, docs/web/web.md "Paging hubs"): Browse by Computer, Project or Space
// arranges the list in folding sections, a repository on two computers is one project that lists both directories,
// and the value pill's menu is as wide as its pill. The Agents scope excludes Persona and health conversations (#4676).
//
// Needs the web page against the fake hub with the paged list, its catalog and one assistant, and Chrome:
//
//   MOCK_SESSIONS_LIST=1 MOCK_SESSIONS_CATALOG=1 MOCK_SESSIONS_MANY=14 MOCK_MANAGERS=1 PORT=18793 python3 scripts/mock/mock_hub.py &
//   (cd web && PERSONA_HUB_URL=http://127.0.0.1:18793 npx vite --port 5293 --strictPort &)
//   node web/e2e/agents-browse-by.mjs http://127.0.0.1:5293 <artifact directory> [--hub http://127.0.0.1:18793] [--channel chrome]
//
// The fake hub pins a space by default, which keeps the sidebar on its complete list: the runner clears the pins (and
// puts them back). With --hub it also counts the reads of a churn of hints while the list is drawn by project. Prints
// one line per check and a verdict; exits 1 on a failed check and 2 on its three-minute timeout.
import { mkdirSync } from "node:fs";
import { chromium } from "playwright-core";

const args = process.argv.slice(2);
const flag = (name, fallback) => {
  const i = args.indexOf(name);
  return i >= 0 ? args[i + 1] : fallback;
};
const base = (args[0] ?? "").replace(/\/$/, "");
const out = args[1];
const hub = (flag("--hub") ?? "").replace(/\/$/, "");
const channel = flag("--channel", "chrome");
if (!base || !out || out.startsWith("--")) {
  console.error("usage: node web/e2e/agents-browse-by.mjs <web URL> <artifact directory> [--hub <fake hub URL>] [--channel chrome]");
  process.exit(2);
}
mkdirSync(out, { recursive: true });
const watchdog = setTimeout(() => { console.log("VERDICT TIMEOUT agents browse by"); process.exit(2); }, 180_000);

const failures = [];
const check = (ok, what, detail = "") => {
  console.log(`${ok ? "ok  " : "FAIL"} ${what}${detail ? `: ${detail}` : ""}`);
  if (!ok) failures.push(what);
};
const hubGet = async (path, init) => (await fetch(`${base}/api/v1${path}`, { ...init, headers: { Authorization: "Bearer mock", "Content-Type": "application/json", ...init?.headers }, signal: AbortSignal.timeout(10_000) })).json();
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

// What the hub says, to hold the page to: the catalog with child sessions folded, as the page asks for it.
const hello = await hubGet("/health").catch(() => ({}));
const pins = await hubGet("/pins");
await hubGet("/pins", { method: "PUT", body: JSON.stringify({ spaces: [], sessions: [] }) });
const [catalog, counts, allRows] = await Promise.all([
  hubGet("/sessions?catalog=1&roots=1&agents_only=1"),
  hubGet("/sessions?counts=1&roots=1&agents_only=1"),
  hubGet("/sessions?summary=1&limit=200&roots=1"),
]);
const persona = allRows.sessions?.find((row) => row.assistant && row.space !== "health");
const health = allRows.sessions?.find((row) => row.space === "health");
const idleAgent = allRows.sessions?.find((row) => !row.assistant && row.space !== "health" && row.state === "idle" && !row.children);
const machines = (await hubGet("/machines")).machines ?? [];
const machineName = (id) => machines.find((m) => m.id === id)?.name ?? id;
if (!Array.isArray(catalog.projects)) {
  console.log(`VERDICT FAIL the hub at ${base} has no session catalog (start the fake hub with MOCK_SESSIONS_LIST=1 MOCK_SESSIONS_CATALOG=1)`, JSON.stringify(hello).slice(0, 120));
  process.exit(1);
}
check(Boolean(persona && health) && counts.total === allRows.total - allRows.sessions.filter((row) => row.assistant || row.space === "health").length,
  "the unscoped list includes Persona and health while Agents counts exclude them");
const shared = catalog.projects.find((p) => p.origin && new Set(p.places.map((x) => x.machine_id)).size > 1);
const byMachine = {};
for (const p of catalog.projects) for (const place of p.places) byMachine[place.machine_id] = (byMachine[place.machine_id] ?? 0) + place.total;

const browser = await chromium.launch({ channel, headless: true });
try {
  for (const { name, theme, viewport, sidebar, touch } of [
    { name: "wide-light", theme: "light", viewport: { width: 1280, height: 900 }, sidebar: 340 },
    { name: "wide-dark", theme: "dark", viewport: { width: 1280, height: 900 }, sidebar: 340 },
    { name: "narrow-light", theme: "light", viewport: { width: 1024, height: 820 }, sidebar: 240 },
    { name: "phone-dark", theme: "dark", viewport: { width: 390, height: 844 }, touch: true },
  ]) {
    const ctx = await browser.newContext({ viewport, deviceScaleFactor: 2, locale: "en-US", colorScheme: theme, hasTouch: Boolean(touch), isMobile: Boolean(touch) });
    const page = await ctx.newPage();
    const errors = [];
    page.on("pageerror", (e) => errors.push(String(e)));
    await page.goto(`${base}/?token=mock`);
    await page.waitForFunction(() => localStorage.getItem("persona.token"), null, { timeout: 15_000 });
    await page.evaluate(([theme, sidebar]) => {
      localStorage.setItem("persona.theme", theme);
      if (sidebar) localStorage.setItem("persona.sidebarWidth", String(sidebar));
      for (const key of Object.keys(localStorage)) if (key.startsWith("fold:sessions-by:") || key.endsWith("BrowseBy")) localStorage.removeItem(key);
    }, [theme, sidebar ?? 0]);
    await page.goto(touch ? `${base}/agents` : `${base}/`);
    const list = page.locator("[data-paged-sessions='runtime']").first();
    await list.waitFor({ timeout: 20_000 });
    const tab = (label) => list.getByRole("radio", { name: label, exact: true });
    const sections = () => list.locator("[data-session-group]");
    const headings = async () => sections().evaluateAll((els) => els.map((el) => {
      const top = el.querySelector("[data-sidebar-group-heading]");
      return { name: top.querySelector("button[aria-expanded]").textContent, open: top.querySelector("button[aria-expanded]").getAttribute("aria-expanded") === "true",
        count: Number(top.querySelector("span[aria-hidden]").textContent), rows: el.querySelectorAll("[data-session-row]").length };
    }));
    const total = async () => Number((await list.locator("[data-session-total]").textContent()).replace(/\D+/g, ""));
    const waitTotal = (n) => list.locator("[data-session-total]").filter({ hasText: new RegExp(`^${n} sessions$`) }).waitFor({ timeout: 10_000 });
    const shot = async (step) => {
      await page.waitForTimeout(350);
      await list.evaluate((el) => el.scrollIntoView({ block: "start" }));
      await page.waitForTimeout(150);
      await page.screenshot({ path: `${out}/${name}-${step}.png`, ...(touch ? {} : { clip: { x: 0, y: 0, width: (sidebar ?? 340) + 40, height: viewport.height } }) });
    };
    const settled = async () => { await sections().first().waitFor({ timeout: 15_000 }); await page.waitForTimeout(600); };
    await waitTotal(counts.total);
    check((await total()) === counts.total, `${name}: the flat list counts only Agents`);
    if (counts.state.working > 0) {
      const working = list.getByRole("radio", { name: `Working ${counts.state.working}`, exact: true });
      await working.click();
      await waitTotal(counts.state.working);
      await list.getByRole("radio", { name: "All", exact: true }).click();
      await waitTotal(counts.total);
    }
    await shot("0-recent");

    if (name === "wide-light" && persona && health) {
      const search = list.getByRole("textbox").first();
      for (const hidden of [persona, health]) {
        await search.fill(hidden.title);
        await waitTotal(0);
        check((await list.locator("[data-session-row]").count()) === 0, `the Agents search excludes ${hidden.space === "health" ? "health" : "Persona"} conversations`);
        await search.clear();
        await waitTotal(counts.total);
      }
      if (hub && idleAgent) {
        const setState = async (row, state) => {
          const params = new URLSearchParams({ mid: row.machine_id, sid: row.session_id, state });
          const reply = await fetch(`${hub}/mock/session_state?${params}`, { signal: AbortSignal.timeout(10_000) });
          if (!reply.ok) throw new Error(`mock state change failed with ${reply.status}`);
        };
        const waitWorking = (n) => list.getByRole("radio", { name: `Working ${n}`, exact: true }).waitFor({ timeout: 10_000 });
        const waitForScopedRefresh = () => page.waitForResponse((response) => {
          const { pathname, searchParams } = new URL(response.url());
          return pathname === "/api/v1/sessions" && searchParams.get("agents_only") === "1" &&
            (searchParams.has("since") || searchParams.get("counts") === "1");
        }, { timeout: 10_000 });
        const working = counts.state.working ?? 0;
        try {
          const refresh = waitForScopedRefresh();
          await setState(health, "working");
          await refresh;
          await setState(idleAgent, "working");
          await waitWorking(working + 1);
          await waitTotal(counts.total);
          check(true, "the live working count includes the changed Agent and excludes the changed health conversation");
        } finally {
          await setState(health, health.state);
          await setState(idleAgent, idleAgent.state);
        }
        if (working > 0) await waitWorking(working);
      }
    }

    // Computer: one section per computer with sessions, the busiest first, the first three open
    await tab("Computer").click();
    await settled();
    let got = await headings();
    const wantMachines = Object.entries(byMachine).sort(([a, x], [b, y]) => y - x || a.localeCompare(b));
    check(JSON.stringify(got.map((g) => [g.name, g.count])) === JSON.stringify(wantMachines.map(([id, n]) => [machineName(id), n])), `${name}: one section per computer, the busiest first`, JSON.stringify(got.map((g) => [g.name, g.count])));
    check(got.every((g, i) => g.open === (i < 3)) && got.slice(0, 3).every((g) => g.rows === Math.min(6, g.count)), `${name}: the first three sections open on their newest sessions`, JSON.stringify(got.map((g) => [g.open, g.rows])));
    check(got.reduce((sum, g) => sum + g.count, 0) === (await total()) && (await total()) === catalog.total, `${name}: the sections hold every session the list counts`, `${await total()} of ${catalog.total}`);
    await shot("1-computer");

    // Project: the hub's catalog, a repository on two computers as one section that lists both directories
    await tab("Project").click();
    await settled();
    got = await headings();
    const wantProjects = catalog.projects.filter((p) => p.total > 0).map((p) => [p.id ? p.name : "No folder", p.total]);
    check(JSON.stringify(got.map((g) => [g.name, g.count])) === JSON.stringify(wantProjects), `${name}: the projects of the hub's catalog, in its order`, JSON.stringify(got.map((g) => [g.name, g.count])));
    if (shared) {
      const section = sections().filter({ has: page.locator("button[aria-expanded]", { hasText: new RegExp(`^${shared.name}$`) }) }).first();
      if ((await section.locator("button[aria-expanded]").getAttribute("aria-expanded")) !== "true") await section.locator("button[aria-expanded]").click();
      await section.locator("[data-session-row]").first().waitFor({ timeout: 10_000 });
      const where = await section.locator("ul li span.truncate").evaluateAll((els) => els.map((el) => el.getAttribute("title")));
      const want = [shared.origin, ...shared.places.map((x) => `${machineName(x.machine_id)} · ${x.path}`)];
      check(JSON.stringify(where) === JSON.stringify(want), `${name}: ${shared.name} lists its remote and each directory with its computer`, JSON.stringify(where));
      check((await section.locator("[data-session-row]").count()) === Math.min(6, shared.total), `${name}: ${shared.name} lists the sessions of all its directories together`);
    } else check(false, `${name}: the fixture has a repository on two computers`);
    await shot("2-project");

    // the value pill's menu: as wide as the pill, the project's directories under its name
    const pick = list.getByRole("button", { name: "Filter group" });
    await pick.click();
    const menu = page.locator('[role="menu"]');
    await menu.waitFor();
    await page.waitForTimeout(250);
    const [a, m] = [await pick.boundingBox(), await menu.boundingBox()];
    check(Math.abs(a.width - m.width) <= 1 || (a.width < 208 && Math.abs(m.width - 208) <= 1), `${name}: the menu is as wide as its pill`, `pill ${Math.round(a.width)}, menu ${Math.round(m.width)}`);
    if (shared) {
      const item = menu.getByRole("menuitemradio").filter({ hasText: shared.origin }).first();
      const text = (await item.textContent()) ?? "";
      check(shared.places.every((x) => text.includes(machineName(x.machine_id))), `${name}: the menu row of ${shared.name} names each computer it is on`, text.replace(/\n/g, " | "));
    }
    await page.waitForTimeout(200);
    await page.screenshot({ path: `${out}/${name}-3-project-menu.png`, ...(touch ? {} : { clip: { x: 0, y: 0, width: (sidebar ?? 340) + 40, height: viewport.height } }) });

    // one project picked: the flat list again, of that project only
    if (shared) {
      await menu.getByRole("menuitemradio").filter({ hasText: shared.origin }).first().click();
      await list.locator("[data-virtual-sessions] [data-session-row]").first().waitFor({ timeout: 10_000 });
      await page.waitForTimeout(400);
      check((await sections().count()) === 0 && (await total()) === shared.total, `${name}: a picked project is the one flat list of its sessions`, `${await total()} of ${shared.total}`);
      await shot("4-project-picked");
      await pick.click();
      await menu.getByRole("menuitemradio").first().click();
      await settled();
    } else await page.keyboard.press("Escape");

    // Space: each computer's spaces by name, the sessions in no space last
    await tab("Space").click();
    await settled();
    got = await headings();
    const inSpaces = catalog.spaces.filter((s) => s.id && s.total > 0);
    const loose = catalog.spaces.filter((s) => !s.id).reduce((sum, s) => sum + s.total, 0);
    check(got.length === inSpaces.length + (loose ? 1 : 0) && (!loose || (got.at(-1).name === "No space" && got.at(-1).count === loose)), `${name}: a section per computer's space, the sessions in no space last`, JSON.stringify(got.map((g) => [g.name, g.count])));
    check(new Set(got.map((g) => g.name)).size === got.length, `${name}: two computers' spaces of one name are told apart`);
    await shot("5-space");

    // the choice is kept: the list opens the way it was last read
    await page.reload();
    await list.waitFor({ timeout: 20_000 });
    await settled();
    check((await tab("Space").getAttribute("aria-checked")) === "true", `${name}: the chosen Browse by is kept in this browser`);
    await tab("Recently updated").click();
    await list.locator("[data-virtual-sessions]").waitFor({ timeout: 10_000 });
    check((await sections().count()) === 0, `${name}: Recently updated is the one flat list`);
    check(errors.length === 0, `${name}: no page errors`, errors.join(" | ").slice(0, 300));

    // how often the hub is read while the list is drawn by project and its rows change (the fake hub's own counters)
    if (hub && name === "wide-light") {
      const mock = async (path) => (await fetch(`${hub}${path}`, { signal: AbortSignal.timeout(10_000) })).json();
      await tab("Project").click();
      await settled();
      await sleep(2_000);
      await mock("/mock/sessions_reads?reset=1");
      const seconds = 20;
      await mock(`/mock/churn?n=${seconds * 2}&ms=500&rows=1`);
      await sleep(seconds * 1000 + 2_000);
      const reads = await mock("/mock/sessions_reads?reset=1");
      const web = reads.reads?.web ?? {};
      const kinds = Object.fromEntries(Object.entries(web).map(([kind, row]) => [kind, row.count]));
      console.log(`reads in ${reads.seconds} s of churn by project: ${reads.hints} hints, ${JSON.stringify(kinds)}`);
      // one read of the groups (with their needs-you counts) per refresh gap, however many hints came
      const ceiling = Math.ceil(reads.seconds / 1.5) + 1;
      check((kinds.catalog ?? 0) <= ceiling, `the groups are read at most once per 1.5 s under ${reads.hints} hints`, `${kinds.catalog ?? 0} catalog reads, ceiling ${ceiling}`);
      check((kinds.full ?? 0) === 0 && (kinds.baseline ?? 0) <= 4, "a churn of hints reads no complete list and no new first pages", JSON.stringify(kinds));
      await tab("Recently updated").click();
    }
    await ctx.close();
  }
} catch (e) {
  check(false, "the run completed", String(e).slice(0, 400));
} finally {
  clearTimeout(watchdog);
  await browser.close();
  await hubGet("/pins", { method: "PUT", body: JSON.stringify(pins) }).catch(() => {});
}
console.log(`VERDICT ${failures.length ? "FAIL" : "PASS"} agents browse by${failures.length ? `: ${failures.length} failed` : ""}; screenshots in ${out}`);
process.exitCode = failures.length ? 1 : 0;
