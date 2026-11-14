// Production client against the dedicated synthetic worktree hub; never use a live hub as the fixture.
// node web/e2e/agent-hierarchy.mjs <web origin> <mock origin> <artifacts>
import assert from "node:assert/strict";
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { chromium } from "playwright-core";

const [web, hub, output] = process.argv.slice(2);
assert([web, hub].every((u) => /^http:\/\/(127\.0\.0\.1|localhost):\d+$/.test(u ?? "")) && output);
assert.equal((await (await fetch(`${hub}/mock/worktree-migration`)).json()).fixture, "worktree-migration");
mkdirSync(output, { recursive: true });
const prefix = "fixture-agent-";
function withHierarchy(sessions) {
  const base = sessions.find((s) => s.machine_id === "macbook" && (s.space_kind === "orca" || s.pane_id?.startsWith("orca:")) && !s.assistant);
  if (!base) return sessions; // an initial/partial manager push can precede the Orca catalog
  return [...sessions.filter((s) => !s.session_id.startsWith(prefix)), ...[
    ["leader", "Hierarchy leader", undefined], ["worker-a", "Worker A", "leader"],
    ["worker-b", "Worker B", "leader"], ["nested", "Nested worker", "worker-a"],
  ].map(([id, title, parent]) => ({ ...base, session_id: prefix + id, title,
    pane_id: `orca:${prefix + id}:pane`, slot_id: prefix + id, tab_id: prefix + id,
    parent_agent_id: parent ? prefix + parent : undefined, parent_session_id: undefined,
    parent_thread_id: undefined, root_session_id: undefined, is_subagent: false,
    status: "working", state: "working", starting: false, archived: false,
    updated_at: new Date().toISOString() }))];
}
const browser = await chromium.launch({ channel: "chrome" });
const results = [];
const watchdog = setTimeout(() => { console.error("TIMEOUT agent hierarchy (180s)"); process.exit(124); }, 180_000);
try {
  for (const width of [1440, 390]) for (const theme of ["light", "dark"]) {
    const context = await browser.newContext({ viewport: { width, height: 900 }, hasTouch: width === 390, colorScheme: theme, reducedMotion: "reduce" });
    await context.addInitScript(({ theme }) => {
      localStorage.setItem("persona.token", "synthetic-worktree-token");
      localStorage.setItem("persona.language", "en");
      localStorage.setItem("persona.theme", theme);
      localStorage.setItem("persona.agentGrouping", "repo");
    }, { theme });
    await context.route("**/api/v1/sessions", async (route) => {
      const response = await route.fetch();
      if (response.status() === 304) return route.fulfill({ response });
      const body = await response.json();
      if (body.sessions) body.sessions = withHierarchy(body.sessions);
      return route.fulfill({ response, json: body });
    });
    await context.routeWebSocket(/.*/, (socket) => {
      const server = socket.connectToServer();
      server.onMessage((raw) => {
        const body = JSON.parse(String(raw));
        if (body.type === "sessions" && body.sessions) body.sessions = withHierarchy(body.sessions);
        socket.send(JSON.stringify(body));
      });
    });
    const page = await context.newPage();
    const errors = [];
    page.on("pageerror", (e) => errors.push(String(e)));
    page.setDefaultTimeout(15000);
    await page.goto(`${web}/agents`);
    const surface = width === 390 ? page.getByRole("main") : page.locator("aside:visible");
    const groups = surface.getByRole("radiogroup", { name: "Group agents by" });
    await groups.waitFor();
    for (const mode of ["Repo", "Project", "Machine", "Status", "To-do"]) {
      await groups.getByRole("radio").filter({ hasText: new RegExp(`^${mode}$`) }).click();
      const expand = surface.getByRole("button", { name: "Expand all", exact: true });
      if (await expand.count()) await expand.click();
      const leader = surface.locator(`[data-agent-parent="s:macbook/${prefix}leader"]`);
      await leader.waitFor();
      const worker = leader.locator(`[data-agent-parent="s:macbook/${prefix}worker-a"]`);
      assert.equal(await worker.getByRole("link", { name: /Nested worker/ }).count(), 1);
      assert.equal(await leader.getByRole("link", { name: /Worker B/ }).count(), 1);
      for (const title of ["Hierarchy leader", "Worker A", "Worker B", "Nested worker"]) {
        assert.equal(await surface.getByRole("link", { name: new RegExp(title) }).count(), 1, `${title} occurs once in ${mode}`);
      }
      const disclosure = leader.locator(":scope > div > button[aria-expanded]");
      await leader.scrollIntoViewIfNeeded();
      await page.screenshot({ path: join(output, `${width}-${theme}-${mode}-open.png`) });
      await disclosure.click();
      assert.equal(await disclosure.getAttribute("aria-expanded"), "false");
      assert(await leader.locator(":scope > div").first().textContent().then((s) => s.includes("3")), "Folded parent counts all three working descendants");
      await disclosure.press("Enter");
      assert.equal(await disclosure.getAttribute("aria-expanded"), "true");
      await disclosure.press("Space");
      assert.equal(await disclosure.getAttribute("aria-expanded"), "false");
      await disclosure.click();
      assert.equal(await disclosure.getAttribute("aria-expanded"), "true");
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
      assert.deepEqual(errors, []);
      results.push({ width, theme, mode, nested: true, unique: true, repeatedKeyboardFold: true });
    }
    await context.close();
  }
} finally { clearTimeout(watchdog); await browser.close(); }
writeFileSync(join(output, "results.json"), JSON.stringify(results, null, 2));
console.log(`PASS ${results.length} agent hierarchy browser combinations`);
