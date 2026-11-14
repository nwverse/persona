// Agent presentation names and tags on the paged and pinned Agents lists (#7527).
// Start the paged mock hub and a production preview, then run:
// node web/e2e/agents-presentation.mjs http://127.0.0.1:5294 <artifact directory> [--chrome /path/to/chrome]
import { existsSync } from "node:fs";
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { chromium } from "playwright-core";

const args = process.argv.slice(2);
const base = (args[0] ?? "").replace(/\/$/, "");
const out = args[1];
const chromeAt = args.indexOf("--chrome");
const executablePath = chromeAt >= 0 ? args[chromeAt + 1] : `${process.env.HOME}/.cache/ms-playwright/chromium-1243/chrome-linux64/chrome`;
if (!base || !out || out.startsWith("--")) {
  console.error("usage: node web/e2e/agents-presentation.mjs <web URL> <artifact directory> [--chrome /path/to/chrome]");
  process.exit(2);
}
mkdirSync(out, { recursive: true });
const watchdog = setTimeout(() => { console.log("VERDICT TIMEOUT agents presentation"); process.exit(2); }, 180_000);
const failures = [];
const check = (ok, what, detail = "") => {
  console.log(`${ok ? "ok  " : "FAIL"} ${what}${detail ? `: ${detail}` : ""}`);
  if (!ok) failures.push(what);
};
const hub = async (path, init = {}) => {
  const response = await fetch(`${base}/api/v1${path}`, { ...init, headers: { Authorization: "Bearer mock", "Content-Type": "application/json", ...init.headers } });
  const body = await response.json();
  if (!response.ok) throw new Error(`${init.method ?? "GET"} ${path}: ${response.status} ${JSON.stringify(body)}`);
  return body;
};
const putPresentation = (session, display_name, tags) => hub(`/sessions/${encodeURIComponent(session.machine_id)}/${encodeURIComponent(session.session_id)}/presentation`, {
  method: "PUT", body: JSON.stringify({ display_name, tags }),
});
const setPins = (pins) => hub("/pins", { method: "PUT", body: JSON.stringify(pins) });

const savedPins = await hub("/pins");
await setPins({ spaces: [], sessions: [] });
const working = (await hub("/sessions?state=working&limit=200&summary=1")).sessions.filter((s) => !s.background_waiting);
const first = working[0];
const second = working.find((s) => s.machine_id !== first?.machine_id);
const third = working.find((s) => s.machine_id !== first?.machine_id && s.machine_id !== second?.machine_id) ?? working.find((s) => s.session_id !== first?.session_id && s.session_id !== second?.session_id);
if (!first || !second || !third) {
  console.log("VERDICT FAIL the mock needs at least three working sessions, two on different computers");
  process.exit(1);
}
const firstName = "Release audit";
const secondName = "Deploy review";
const secondDefault = second.title;
await putPresentation(first, firstName, ["review"]);
await putPresentation(second, secondName, ["release"]);

const browser = await chromium.launch({ headless: true, ...(existsSync(executablePath) ? { executablePath } : { channel: "chrome" }) });
try {
  for (const theme of ["light", "dark"]) {
    await setPins({ spaces: [], sessions: [] });
    await putPresentation(first, firstName, ["review"]);
    await putPresentation(second, secondName, ["release"]);
    const context = await browser.newContext({ viewport: { width: 1280, height: 1500 }, deviceScaleFactor: 2, locale: "en-US", colorScheme: theme, reducedMotion: "reduce" });
    const page = await context.newPage();
    const errors = [];
    page.on("pageerror", (error) => errors.push(String(error)));
    const agents = page.locator("[data-sidebar-agents]").first();
    const load = async () => {
      await page.goto(`${base}/inbox?token=mock`);
      await page.waitForFunction(() => localStorage.getItem("persona.token"), null, { timeout: 15_000 });
      await page.evaluate((scheme) => {
        localStorage.setItem("persona.theme", scheme);
        localStorage.setItem("persona.language", "en");
        localStorage.setItem("persona.sidebarWidth", "360");
        localStorage.setItem("persona.agentGrouping", "machine");
      }, theme);
      await page.goto(`${base}/`);
      await page.locator("aside").first().waitFor({ timeout: 15_000 });
      await page.waitForTimeout(900);
    };
    const selectTags = async () => {
      await page.getByTestId("agent-tag-filter-button").first().click();
      const menu = page.locator('[role="menu"]').last();
      for (const tag of ["review", "release"]) {
        await menu.locator('[role^="menuitem"]').filter({ hasText: new RegExp(`^${tag}`) }).click();
      }
      await page.keyboard.press("Escape");
      await page.waitForTimeout(700);
    };
    const shot = async (name) => {
      if (name === "editor") {
        await page.waitForTimeout(300);
        await page.screenshot({ path: join(out, `${name}-${theme}.png`), animations: "disabled" });
        await page.evaluate(() => { document.body.style.zoom = "200%"; });
        await page.waitForTimeout(250);
        await page.screenshot({ path: join(out, `${name}-large-${theme}.png`), animations: "disabled" });
        await page.evaluate(() => { document.body.style.zoom = ""; });
        await page.waitForTimeout(150);
        return;
      }
      const side = await page.locator("aside").first().boundingBox();
      await agents.evaluate((el) => el.scrollIntoView({ block: "start" }));
      await page.waitForTimeout(300);
      await page.screenshot({ path: join(out, `${name}-${theme}.png`), clip: { x: side.x, y: Math.max(0, side.y), width: side.width, height: Math.min(1200, 1500 - side.y) }, animations: "disabled" });
      await page.evaluate(() => { document.body.style.zoom = "200%"; });
      await page.waitForTimeout(250);
      await agents.screenshot({ path: join(out, `${name}-large-${theme}.png`), animations: "disabled" });
      await page.evaluate(() => { document.body.style.zoom = ""; });
      await page.waitForTimeout(150);
    };
    await load();
    check(await agents.getByText(firstName, { exact: true }).count() === 1, `${theme}: custom name appears before filtering`);
    check(await agents.getByText(secondName, { exact: true }).count() === 1, `${theme}: second custom name appears before filtering`);
    await selectTags();
    check(await page.locator('[data-chip="tags:review"]').count() === 1 && await page.locator('[data-chip="tags:release"]').count() === 1, `${theme}: two removable tag chips coexist with quick filters`);
    const ensureWorking = async () => {
      const chip = page.getByRole("radio", { name: /^Working(?: |$)/ }).first();
      if (await chip.getAttribute("aria-checked") !== "true") await chip.click();
      await page.waitForTimeout(700);
    };
    await ensureWorking();
    check(await agents.getByText(firstName, { exact: true }).count() === 1 && await agents.getByText(secondName, { exact: true }).count() === 1, `${theme}: multi-tag OR composes with Working`);
    check(await agents.getByText(third.title, { exact: true }).count() === 0, `${theme}: untagged working session is filtered out`);
    check(await page.locator("[data-session-group]").count() > 0, `${theme}: tag results keep computer groups`);
    await shot("paged-tags");

    await setPins({ spaces: [], sessions: [`${first.machine_id}/${first.session_id}`] });
    await load();
    await selectTags();
    await ensureWorking();
    check(await agents.getByText(firstName, { exact: true }).count() === 1, `${theme}: pinned match appears exactly once`);
    check(await agents.getByText("Pinned", { exact: true }).count() > 0, `${theme}: pinned section remains under tag filtering`);
    check(await agents.getByText(secondName, { exact: true }).count() === 1, `${theme}: computer/project group remains beside pinned section`);
    await shot("pinned-tags");

    const row = agents.getByText(secondName, { exact: true });
    const rowRoot = row.locator("xpath=ancestor::div[contains(@class,'group/row')][1]");
    await rowRoot.hover();
    await rowRoot.getByRole("button", { name: "Agent actions" }).click();
    await page.getByRole("menuitem", { name: "Name & tags…" }).click();
    const dialog = page.getByRole("dialog");
    await dialog.waitFor();
    const nameField = dialog.locator("input").first();
    await nameField.fill("Deploy follow-up");
    await dialog.getByRole("button", { name: "Remove tag release" }).click();
    await dialog.locator("textarea").fill("ops, release");
    await dialog.getByRole("button", { name: "Add tags" }).click();
    await shot("editor");
    await dialog.getByRole("button", { name: "Save" }).click();
    await dialog.waitFor({ state: "detached" });
    let stored = (await hub(`/sessions?machine_id=${encodeURIComponent(second.machine_id)}&session_id=${encodeURIComponent(second.session_id)}&summary=0&limit=1`)).sessions[0];
    check(stored.display_name === "Deploy follow-up" && stored.tags.join(",") === "ops,release", `${theme}: rename and tag edit persist through the hub`, JSON.stringify({ name: stored.display_name, tags: stored.tags }));
    await page.reload();
    await page.waitForTimeout(900);
    check(await agents.getByText("Deploy follow-up", { exact: true }).count() === 1, `${theme}: edited name survives a browser refresh`);

    const editedRow = agents.getByText("Deploy follow-up", { exact: true });
    const editedRoot = editedRow.locator("xpath=ancestor::div[contains(@class,'group/row')][1]");
    await editedRoot.hover();
    await editedRoot.getByRole("button", { name: "Agent actions" }).click();
    await page.getByRole("menuitem", { name: "Name & tags…" }).click();
    const clearing = page.getByRole("dialog");
    await clearing.locator("input").first().fill("");
    await clearing.getByRole("button", { name: "Save" }).click();
    await clearing.waitFor({ state: "detached" });
    stored = (await hub(`/sessions?machine_id=${encodeURIComponent(second.machine_id)}&session_id=${encodeURIComponent(second.session_id)}&summary=0&limit=1`)).sessions[0];
    check(!stored.display_name && stored.title === secondDefault, `${theme}: clearing restores the default title`, stored.title);
    check(errors.length === 0, `${theme}: no page errors`, errors.join(" | "));
    await context.close();
  }
} finally {
  await setPins(savedPins).catch(() => {});
  await browser.close();
  clearTimeout(watchdog);
}
console.log(failures.length ? `VERDICT FAIL ${failures.length} checks` : "VERDICT PASS agents presentation (synthetic data only)");
process.exit(failures.length ? 1 : 0);
