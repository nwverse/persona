// Run against the loopback mock with MOCK_SAVED_TODO_VIEWS=1, or legacy with --legacy.
// node web/e2e/saved-todo-views.mjs [--legacy] [--shots <dir>]
// Set TODO_WEB and TODO_HUB to override the default loopback ports.
import assert from "node:assert/strict";
import { mkdir } from "node:fs/promises";
import { join } from "node:path";
import { chromium } from "playwright-core";

const web = process.env.TODO_WEB ?? "http://127.0.0.1:5797";
const hub = process.env.TODO_HUB ?? "http://127.0.0.1:18797";
const origins = new Set([web, hub].map((url) => {
  const parsed = new URL(url);
  assert.equal(parsed.hostname, "127.0.0.1", "use only the loopback mock");
  return parsed.origin;
}));
const legacy = process.argv.includes("--legacy");
const index = process.argv.indexOf("--shots");
const shots = index === -1 ? undefined : process.argv[index + 1];
if (shots) await mkdir(shots, { recursive: true });

const path = "/api/v1/assistant/saved-todo-views";
const later = "Review travel plans";
const early = "Review the changelog";
const late = "Review metrics";
const unrelated = "Draft an outline";
const saved = "Review queue";
const updated = "Unscheduled";
const errors = [];

async function request(endpoint = path, { method = "GET", body, status = 200, manager } = {}) {
  const response = await fetch(hub + endpoint, {
    method,
    headers: { Authorization: "Bearer mock", "Content-Type": "application/json",
      ...(manager && { "X-Persona-Manager": manager }) },
    ...(body && { body: JSON.stringify(body) }),
    signal: AbortSignal.timeout(8000),
  });
  assert.equal(response.status, status, `${method} ${endpoint}`);
  return response.json();
}

const browser = await chromium.launch({ channel: "chrome", headless: true });
async function open(width, theme) {
  const context = await browser.newContext({
    viewport: { width, height: 830 }, timezoneId: "UTC", colorScheme: theme,
    reducedMotion: "reduce", hasTouch: width < 600,
  });
  await context.route("**/*", (route) => origins.has(new URL(route.request().url()).origin) ? route.continue() : route.abort());
  await context.addInitScript((mode) => {
    localStorage.setItem("persona.theme", mode);
    localStorage.setItem("persona.language", "en");
  }, theme);
  const page = await context.newPage();
  page.on("pageerror", (error) => errors.push(error.message));
  page.setDefaultTimeout(15000);
  await page.goto(web + "/todos?token=mock");
  await task(page, unrelated).waitFor();
  return page;
}

const task = (page, name) => page.getByText(name, { exact: true }).first();
const listControls = (page) => page.getByRole("button", { name: "Filter and sort", exact: true });
const search = (page) => page.getByPlaceholder("Search to-dos", { exact: true });
const menu = (page, name) => page.getByRole("menuitem", { name, exact: true });
const choice = (page, name) => page.getByRole("menuitemradio", { name });
const manager = (page) => page.getByRole("button", { name: /switch assistant/i }).first();

async function pick(page, name) {
  await listControls(page).click();
  await choice(page, name).click();
}

async function library(page) {
  await listControls(page).click();
  await menu(page, "Saved views").click();
  const dialog = page.getByRole("dialog", { name: "Saved views", exact: true });
  await dialog.waitFor();
  return dialog;
}

const result = (page) => page.waitForResponse((res) => res.request().method() === "POST" && new URL(res.url()).pathname === path);
async function save(page, dialog) {
  const posted = result(page);
  await dialog.getByRole("button", { name: "Save view", exact: true }).click();
  const response = await posted;
  assert.equal(response.status(), 200);
  return response.json();
}

const shot = (page, filename) => shots && page.screenshot({ path: join(shots, filename + ".png"), animations: "disabled" });

try {
  if (!legacy) await request("/mock/saved-todo-views/reset", { method: "POST", body: {} });
  const desktop = await open(1200, "light");
  if (legacy) {
    await request(path, { status: 404 });
    await listControls(desktop).click();
    assert.equal(await menu(desktop, "Saved views").count(), 0);
    assert.equal(await menu(desktop, "Save current view").count(), 0);
    await choice(desktop, /^No due date/).click();
    await task(desktop, unrelated).waitFor();
    await task(desktop, early).waitFor({ state: "hidden" });
    console.log("PASS an older manager keeps its ordinary filters");
  } else {
    const phone = await open(390, "dark");
    const initial = await request();
    assert.deepEqual(initial.views, []);
    // On a cold narrow page the dialog takes over the filter menu's Back entry.
    const firstLibrary = await library(phone);
    await firstLibrary.getByText("No saved views yet", { exact: true }).waitFor();
    await phone.evaluate(() => history.back());
    await firstLibrary.waitFor({ state: "hidden" });
    assert.ok(new URL(phone.url()).pathname.endsWith("/todos"), "Back closes the dialog without leaving To-dos");
    const phoneLibrary = await library(phone);
    await search(desktop).fill("review");
    await pick(desktop, /^Today/);
    await pick(desktop, /^Due date/);
    await task(desktop, early).waitFor();
    await task(desktop, late).waitFor();
    await task(desktop, later).waitFor({ state: "hidden" });
    await task(desktop, unrelated).waitFor({ state: "hidden" });
    await listControls(desktop).click();
    await menu(desktop, "Save current view").click();
    let dialog = desktop.getByRole("dialog", { name: "Save current view", exact: true });
    await dialog.getByRole("textbox", { name: "View name" }).fill(saved);
    const created = await save(desktop, dialog);
    assert.equal(created.views[0].name, saved);
    assert.deepEqual(created.views[0].criteria, { text: "review", due_window: "today", section_sort: "due" });
    await dialog.waitFor({ state: "hidden" });
    await desktop.getByRole("dialog", { name: "Saved views", exact: true }).getByRole("button", { name: "Done", exact: true }).click();
    await request(path, { method: "POST", status: 409, body: {
      revision: initial.revision, op: "delete", id: created.views[0].id,
    } });
    assert.deepEqual((await request()).views, created.views, "a stale mutation leaves the current view intact");
    assert.deepEqual((await request(path, { manager: "m-3fa21c" })).views, []);

    dialog = phoneLibrary;
    await dialog.getByRole("button", { name: `Apply ${saved}`, exact: true }).click();
    await task(phone, early).waitFor();
    await task(phone, late).waitFor();
    await task(phone, later).waitFor({ state: "hidden" });
    await task(phone, unrelated).waitFor({ state: "hidden" });
    assert.ok(new URL(phone.url()).pathname.endsWith("/todos"), "applying keeps the To-dos route");
    await shot(phone, "todo-saved-view-applied-dark");

    dialog = await library(phone);
    await dialog.getByRole("button", { name: `Edit ${saved}`, exact: true }).click();
    const phoneDraft = phone.getByRole("dialog", { name: "Edit saved view", exact: true });
    await phoneDraft.getByRole("textbox", { name: "View name" }).fill(updated);

    await search(desktop).clear();
    await pick(desktop, /^No due date/);
    dialog = await library(desktop);
    await dialog.getByRole("button", { name: `Edit ${saved}`, exact: true }).click();
    dialog = desktop.getByRole("dialog", { name: "Edit saved view", exact: true });
    await dialog.getByRole("button", { name: "Use current filters", exact: true }).click();
    const changed = await save(desktop, dialog);
    assert.equal(changed.views[0].id, created.views[0].id);
    assert.equal(changed.views[0].name, saved);
    assert.deepEqual(changed.views[0].criteria, { text: "", due_window: "no_due", section_sort: "due" });

    await phoneDraft.getByRole("button", { name: "Reload saved views", exact: true }).click();
    assert.equal(await phoneDraft.getByRole("textbox", { name: "View name" }).inputValue(), updated);
    const renamed = await save(phone, phoneDraft);
    assert.equal(renamed.views[0].name, updated);
    assert.deepEqual(renamed.views[0].criteria, changed.views[0].criteria, "renaming preserves concurrently updated filters");
    dialog = phone.getByRole("dialog", { name: "Saved views", exact: true });
    await dialog.getByRole("button", { name: `Apply ${updated}`, exact: true }).click();
    await task(phone, unrelated).waitFor();
    await task(phone, early).waitFor({ state: "hidden" });
    await task(phone, late).waitFor({ state: "hidden" });
    await task(phone, later).waitFor({ state: "hidden" });

    dialog = desktop.getByRole("dialog", { name: "Saved views", exact: true });
    await dialog.getByRole("button", { name: `Apply ${updated}`, exact: true }).waitFor();
    await shot(desktop, "todo-saved-view-library-light");
    await dialog.getByRole("button", { name: "Done", exact: true }).click();
    await manager(desktop).click();
    await choice(desktop, /^工作/).click();
    await desktop.waitForFunction(() => localStorage.getItem("persona.currentManager") === "m-3fa21c");
    const namedRead = desktop.waitForResponse((response) => new URL(response.url()).pathname === path &&
      response.request().method() === "GET" && response.request().headers()["x-persona-manager"] === "m-3fa21c");
    await desktop.goto(web + "/todos");
    assert.deepEqual((await (await namedRead).json()).views, []);
    dialog = await library(desktop);
    await dialog.getByText("No saved views yet", { exact: true }).waitFor();
    assert.equal(await dialog.getByRole("button", { name: `Apply ${updated}`, exact: true }).count(), 0);
    assert.deepEqual((await request(path, { manager: "m-3fa21c" })).views, []);

    dialog = await library(phone);
    const removed = result(phone);
    await dialog.getByRole("button", { name: `Delete ${updated}`, exact: true }).click();
    await phone.getByRole("dialog", { name: "Delete saved view", exact: true }).getByRole("button", { name: "Delete", exact: true }).click();
    assert.equal((await removed).status(), 200);
    assert.deepEqual((await request()).views, []);
    assert.equal((await request("/api/v1/todos")).todos.length, 4, "deleting a view keeps every task");
    console.log("PASS save, relative filters, cross-device apply and update, revision conflict, manager isolation and deletion");
  }
  assert.deepEqual(errors, []);
} finally {
  await browser.close();
}
