// Settings fixes from the web usability audit (audit items 17, 18, 19, 20, 64, 66, 67 and 68), checked in real Chrome
// against an owned loopback web server backed by scripts/mock/mock_hub.py (MOCK_EXTENSIONS=1 MOCK_CHANNELS=1).
// Usage: node web/e2e/settings-audit.mjs <loopback-web-origin> <artifact-directory>
// Prints one line per check and a final verdict; exits nonzero on a failure or after five minutes.
import assert from "node:assert/strict";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { chromium } from "playwright-core";

const [address, artifacts, ...extra] = process.argv.slice(2);
assert.ok(address && artifacts && !extra.length, "Supply the loopback web origin and an artifact directory");
const base = new URL(address);
assert.ok(["127.0.0.1", "localhost", "[::1]"].includes(base.hostname), "Use a loopback web server");
assert.ok(["http:", "https:"].includes(base.protocol) && !base.username && !base.password && base.pathname === "/" && !base.search && !base.hash, "Use an origin without credentials or a route");
const health = await (await fetch(new URL("/api/v1/health", base), { signal: AbortSignal.timeout(5000) })).json();
assert.equal(health.version, "mock", "The web server must be backed by scripts/mock/mock_hub.py");
const output = resolve(artifacts);
await mkdir(output, { recursive: true });

const watchdog = setTimeout(() => {
  console.log("TIMEOUT settings audit: no verdict within five minutes");
  process.exit(2);
}, 300_000);

const locale = async (lang, ns) => JSON.parse(await readFile(new URL(`../src/i18n/locales/${lang}/${ns}.json`, import.meta.url), "utf8"));
const strings = async (lang) => ({
  nav: await locale(lang, "settings-nav"),
  settings: await locale(lang, "settings"),
  language: await locale(lang, "language"),
  common: await locale(lang, "common"),
  hubs: await locale(lang, "hubs"),
  libHubs: await locale(lang, "lib-hubs"),
  confirm: await locale(lang, "disconnect-confirm"),
  wizard: await locale(lang, "settings-providerwizard"),
});
// the back chevron's path (ui/icons.tsx), to count the ways back a page draws
const chevronLeft = /chevronLeft:\s*<path d="([^"]+)"/.exec(await readFile(new URL("../src/ui/icons.tsx", import.meta.url), "utf8"))?.[1];
assert.ok(chevronLeft, "ui/icons.tsx has a chevronLeft path");

const results = { checks: [], runs: [], screenshots: [] };
const check = (name, ok, detail = "") => {
  results.checks.push({ name, ok: !!ok, detail });
  console.log(`${ok ? "PASS" : "FAIL"} ${name}${detail ? ` — ${detail}` : ""}`);
};
const browser = await chromium.launch({ channel: "chrome", headless: true });
const live = new Set();

/** One scenario: a control that never shows up fails it, and the next scenario still runs. */
async function scenario(name, fn) {
  try {
    await fn();
  } catch (e) {
    check(`${name}: ran to the end`, false, String(e?.message ?? e).split("\n")[0]);
  } finally {
    for (const context of live) await context.close().catch(() => {});
    live.clear();
  }
}

/** A fresh context signed in to the mock, in `lang` and `theme`; writes the checks never make are refused. */
async function open(lang, theme, path, { phone = false, height = 820 } = {}) {
  const run = { lang, theme, phone, path, writes: [], refused: [], errors: [], blocked: [] };
  results.runs.push(run);
  const context = await browser.newContext({ viewport: phone ? { width: 390, height: 844 } : { width: 1280, height }, hasTouch: phone, colorScheme: theme, deviceScaleFactor: 1 });
  await context.addInitScript(({ lang, theme }) => {
    if (sessionStorage.getItem("settings-audit")) return;
    sessionStorage.setItem("settings-audit", "1");
    localStorage.setItem("persona.token", "mock");
    localStorage.setItem("persona.language", lang);
    localStorage.setItem("persona.theme", theme);
  }, { lang, theme });
  await context.route("**/*", (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (url.origin !== base.origin) {
      run.blocked.push(url.host);
      return route.abort();
    }
    if (url.pathname.startsWith("/api/") && !["GET", "HEAD", "OPTIONS"].includes(request.method())) {
      const write = `${request.method()} ${url.pathname}`;
      run.writes.push(write);
      // revoking this browser, saving a provider: nothing here may do either
      if (/\/auth\/tokens|\/launchers|\/providers|\/model-config/.test(url.pathname)) {
        run.refused.push(write);
        return route.fulfill({ status: 500, json: { error: "settings-audit: unexpected write" } });
      }
    }
    return route.continue();
  });
  live.add(context);
  const page = await context.newPage();
  page.on("pageerror", (e) => run.errors.push(e.message));
  await page.goto(new URL(path, base).href);
  return { page, run };
}
const shoot = async (page, name) => {
  const file = `${name}.png`;
  await page.screenshot({ path: join(output, file) });
  results.screenshots.push(file);
};

// audit item 66: the hub's category says "This hub" in each language, and Models and Knowledge have their own icons;
// audit item 18: the search box finds a setting by its row's name, Enter opens the first result, Esc clears it.
for (const lang of ["en", "zh-Hans", "ja"]) {
  await scenario(`66/18 ${lang}`, async () => {
    const s = await strings(lang);
    const { page, run } = await open(lang, "light", "/settings?tab=general");
    const box = page.getByLabel(s.settings.search_settings, { exact: true });
    await box.waitFor({ timeout: 15_000 });
    const nav = await page.$$eval("button[aria-controls] span[title]", (spans) => spans.map((s) => s.getAttribute("title")));
    check(`66 ${lang}: the hub's category is "${s.nav.tab_hub}"`, nav.includes(s.nav.tab_hub) && !nav.includes("hub"), nav.join(", "));
    const icon = (label) => page.locator(`button[aria-controls]:has(span[title="${label}"]) svg`).first().innerHTML();
    check(`66 ${lang}: Models and Knowledge selection have different icons`, (await icon(s.nav.tab_models)) !== (await icon(s.nav.tab_knowledge)));
    await box.fill(s.language.label);
    await page.waitForTimeout(300);
    const first = page.locator("nav button").first();
    const found = (await first.count()) > 0 && !(await first.evaluate((b) => b.hasAttribute("aria-controls"))) && (await first.textContent()).includes(s.language.label);
    check(`18 ${lang}: "${s.language.label}" finds the Language setting first`, found);
    await box.press("Enter");
    const lit = await page.waitForFunction((label) => {
      const row = document.querySelector(`select[aria-label="${label}"]`)?.closest("div.py-2\\.5");
      if (!(row instanceof HTMLElement) || !row.style.boxShadow.includes("inset")) return false;
      const r = row.getBoundingClientRect();
      return r.top >= 0 && r.bottom <= innerHeight;
    }, s.language.label, { timeout: 3000 }).then(() => true, () => false);
    check(`18 ${lang}: Enter scrolls to the Language row and lights it`, lit);
    await shoot(page, `18-search-enter-${lang}`);
    await box.fill("xyz");
    await box.press("Escape");
    check(`18 ${lang}: Esc clears the search box`, (await box.inputValue()) === "" && new URL(page.url()).pathname === "/settings");
    check(`${lang}: no page errors`, !run.errors.length, run.errors.join(" | "));
  });
}

const zh = await strings("zh-Hans");
for (const theme of ["light", "dark"]) {
  // audit item 17: General's hints are whole, and Translation's note keeps its distance from Agent preferences
  for (const phone of [false, true]) {
    const where = `${theme}${phone ? " phone" : ""}`;
    await scenario(`17 ${where}`, async () => {
      const { page } = await open("zh-Hans", theme, "/settings?tab=general", { phone });
      await page.locator('[data-settings-group="agent-preferences"]').waitFor({ timeout: 15_000 });
      await page.locator('[data-settings-group="this-browser"]').waitFor({ timeout: 15_000 });
      await page.waitForTimeout(500);
      // the sentences under the settings (the sidebar pages list's one-line states are a list's, not explanations)
      const cut = await page.evaluate(() => {
        const content = document.querySelector('[data-settings-group="appearance"]')?.closest(".overflow-y-auto");
        return [...(content?.querySelectorAll("div.mt-0\\.5.text-ink-3") ?? [])]
          .filter((hint) => !hint.closest('[data-settings-group="sidebar-pages"]') && hint.scrollWidth > hint.clientWidth + 1)
          .map((hint) => hint.textContent.slice(0, 24));
      });
      check(`17 ${where}: General's row hints are not cut off`, !cut.length, cut.join(" | "));
      const gap = await page.evaluate(() => {
        const note = [...document.querySelectorAll("#settings-translation p")].at(-1);
        const next = document.querySelector('[data-settings-group="agent-preferences"]');
        return note && next ? Math.round(next.getBoundingClientRect().top - note.getBoundingClientRect().bottom) : null;
      });
      check(`17 ${where}: Translation's note is spaced from Agent preferences`, gap !== null && gap >= 16, `${gap} px`);
      if (phone) {
        const stacked = await page.evaluate((label) => {
          const select = document.querySelector(`select[aria-label="${label}"]`);
          const hint = select?.closest("div.py-2\\.5")?.querySelector("div.mt-0\\.5")?.getBoundingClientRect();
          return !!hint && !!select && select.getBoundingClientRect().top >= hint.bottom - 1;
        }, (await locale("zh-Hans", "language")).label);
        check(`17 ${where}: the language menu sits under its sentence`, stacked);
        await shoot(page, `17-general-phone-${theme}`);
      } else {
        await page.evaluate(() => document.querySelector('[data-settings-group="agent-preferences"]')?.scrollIntoView({ block: "center" }));
        await shoot(page, `17-general-${theme}`);
      }
    });
  }

  // audit item 19: Next with nothing filled in says what each field needs under it, focusing and showing the first
  await scenario(`19 ${theme}`, async () => {
    const { page, run } = await open("zh-Hans", theme, "/settings?tab=models", { height: 600 });
    await page.getByRole("button", { name: zh.settings.add_provider }).first().click({ timeout: 15_000 });
    await page.getByText(zh.wizard.custom, { exact: true }).first().click({ timeout: 10_000 });
    await page.getByRole("button", { name: zh.wizard.next, exact: true }).click({ timeout: 10_000 });
    const shown = await page.waitForFunction(() => {
      const el = document.activeElement;
      if (!(el instanceof HTMLElement) || el.getAttribute("aria-invalid") !== "true") return false;
      const note = document.getElementById(el.getAttribute("aria-describedby") ?? "");
      if (!note?.textContent?.trim()) return false;
      let scroller = note.parentElement;
      while (scroller && !/(auto|scroll)/.test(getComputedStyle(scroller).overflowY)) scroller = scroller.parentElement;
      const area = (scroller ?? document.documentElement).getBoundingClientRect();
      const r = note.getBoundingClientRect();
      const f = el.getBoundingClientRect();
      return f.top >= area.top - 1 && r.bottom <= area.bottom + 1 ? { field: el.closest("[data-provider-field]")?.getAttribute("data-provider-field"), note: note.textContent.trim() } : false;
    }, null, { timeout: 3000 }).then((h) => h.jsonValue(), () => null);
    check(`19 ${theme}: the first field with a problem is focused, marked invalid and its note is in view`, !!shown, shown ? `${shown.field}: ${shown.note}` : "");
    const flagged = await page.locator('[role="dialog"] [aria-invalid="true"]').count();
    check(`19 ${theme}: every field that needs something says so`, flagged >= 3, `${flagged} fields`);
    const edged = await page.evaluate(() => {
      const probe = document.body.appendChild(document.createElement("span"));
      probe.style.color = "var(--red)";
      const red = getComputedStyle(probe).color;
      probe.remove();
      const others = [...document.querySelectorAll('[role="dialog"] [aria-invalid="true"]')].filter((e) => e !== document.activeElement);
      return others.length > 0 && others.every((e) => getComputedStyle(e).boxShadow.includes(red));
    });
    check(`19 ${theme}: the other fields with a problem have a red edge`, edged);
    check(`19 ${theme}: nothing was saved`, !run.refused.length, run.refused.join(", "));
    await shoot(page, `19-wizard-next-${theme}`);
  });

  // audit item 20: Disconnect asks first, and Cancel revokes nothing
  await scenario(`20 ${theme}`, async () => {
    const { page, run } = await open("zh-Hans", theme, "/settings?tab=general");
    const disconnect = page.getByRole("button", { name: zh.settings.disconnect_browser, exact: true });
    await disconnect.scrollIntoViewIfNeeded({ timeout: 15_000 });
    await disconnect.click();
    const dialog = page.getByRole("dialog", { name: zh.confirm.title });
    const asked = await dialog.waitFor({ timeout: 5000 }).then(() => true, () => false);
    check(`20 ${theme}: Disconnect asks first, saying pairing is needed again`, asked && (await dialog.textContent()).includes(zh.confirm.body), run.refused.join(", "));
    await shoot(page, `20-disconnect-${theme}`);
    if (asked) await dialog.getByRole("button", { name: zh.common.cancel, exact: true }).click();
    const gone = asked && (await dialog.waitFor({ state: "hidden", timeout: 3000 }).then(() => true, () => false));
    const token = await page.evaluate(() => localStorage.getItem("persona.token"));
    check(`20 ${theme}: Cancel closes it, revokes nothing and keeps this browser signed in`, gone && !run.refused.length && token === "mock", run.refused.join(", "));
  });

  // audit item 67: Add hub refuses what is not an address, under the field, and saves nothing
  await scenario(`67 ${theme}`, async () => {
    const { page } = await open("zh-Hans", theme, "/settings?tab=general");
    const add = page.getByRole("button", { name: zh.hubs.add_hub, exact: true });
    await add.scrollIntoViewIfNeeded({ timeout: 15_000 });
    const before = await page.evaluate(() => localStorage.getItem("persona.hubs"));
    const dialog = page.getByRole("dialog", { name: zh.hubs.add_hub });
    for (const typed of ["not a url", "https://hub%20one.example.com"]) {
      await add.click();
      await dialog.getByLabel(zh.hubs.address).first().fill(typed);
      await page.keyboard.press("Enter");
      const said = await dialog.getByText(zh.libHubs.url_invalid, { exact: true }).waitFor({ timeout: 3000 }).then(() => true, () => false);
      const after = await page.evaluate(() => localStorage.getItem("persona.hubs"));
      check(`67 ${theme}: "${typed}" is refused under the field and not saved`, said && (await dialog.isVisible()) && after === before, after ?? "");
      if (typed === "not a url") await shoot(page, `67-add-hub-${theme}`);
      if (await dialog.isVisible()) {
        await dialog.getByRole("button", { name: zh.common.cancel, exact: true }).click();
        await dialog.waitFor({ state: "hidden", timeout: 3000 });
      }
      await page.evaluate((list) => (list === null ? localStorage.removeItem("persona.hubs") : localStorage.setItem("persona.hubs", list)), before);
    }
    await add.click();
    await dialog.getByLabel(zh.hubs.address).first().fill("hub.example.com");
    await page.keyboard.press("Enter");
    await dialog.waitFor({ state: "hidden", timeout: 3000 }).catch(() => {});
    const saved = await page.evaluate(() => localStorage.getItem("persona.hubs") ?? "");
    check(`67 ${theme}: a real address is still added`, saved.includes("https://hub.example.com"));
  });

  // audit item 64: on a phone an item's page has one way back, the top bar's, which steps out to its category
  await scenario(`64 ${theme} phone`, async () => {
    const { page } = await open("zh-Hans", theme, "/settings?tab=connectors", { phone: true });
    const backs = () => page.evaluate((d) => [...document.querySelectorAll("button")].filter((b) => {
      const r = b.getBoundingClientRect();
      return r.width > 0 && r.height > 0 && b.querySelector(`svg path[d="${d}"]`);
    }).length, chevronLeft);
    await page.locator('[data-connector-row="github"]').first().click({ timeout: 15_000 });
    await page.waitForURL(/connector=github/, { timeout: 5000 });
    await page.waitForTimeout(800);
    let n = await backs();
    check(`64 ${theme}: a connector's page on a phone has one Back`, n === 1, `${n} found`);
    await shoot(page, `64-connector-phone-${theme}`);
    await page.getByRole("button", { name: zh.settings.back, exact: true }).click();
    const toCategory = await page.waitForURL((u) => u.searchParams.get("tab") === "connectors" && !u.searchParams.get("connector"), { timeout: 3000 }).then(() => true, () => false);
    check(`64 ${theme}: that Back goes to Connectors`, toCategory, page.url());
    await page.goto(new URL("/settings?tab=connectors&connector=-sent", base).href);
    await page.waitForTimeout(1200);
    n = await backs();
    check(`64 ${theme}: Sent on a phone has one Back`, n === 1, `${n} found`);
    await page.goto(new URL("/settings?tab=extensions", base).href);
    await page.locator("[data-extension-row]").first().click({ timeout: 15_000 });
    await page.waitForTimeout(1000);
    n = await backs();
    check(`64 ${theme}: an extension's page on a phone has one Back`, n === 1, `${n} found`);
  });
  await scenario(`64 ${theme} wide`, async () => {
    const { page } = await open("zh-Hans", theme, "/settings?tab=connectors&connector=github");
    await page.waitForTimeout(1500);
    const own = await page.locator("button").filter({ has: page.locator(`svg path[d="${chevronLeft}"]`) }).filter({ visible: true }).count();
    check(`64 ${theme}: a wide screen keeps the page's own Back`, own >= 1, `${own} found`);
  });

  // audit item 68: a list beside the open item marks it and keeps its name whole, with the state under it
  for (const [tab, row] of [["extensions", "[data-extension-row]"], ["connectors", '[data-connector-row="github"]']]) {
    await scenario(`68 ${theme} ${tab}`, async () => {
      const { page } = await open("zh-Hans", theme, `/settings?tab=${tab}`);
      await page.locator(row).first().waitFor({ timeout: 15_000 });
      check(`68 ${theme} ${tab}: a list on its own marks nothing`, (await page.locator('[aria-current="true"]').count()) === 0);
      await page.locator(row).first().click();
      await page.waitForTimeout(1200);
      const current = await page.evaluate(() => {
        const marked = [...document.querySelectorAll('[aria-current="true"]')];
        const r = marked[0];
        if (!r) return { count: 0 };
        const leaves = [...r.querySelectorAll("span, div")].filter((e) => !e.children.length && e.textContent.trim());
        const [name, state] = leaves;
        return {
          count: marked.length,
          name: name?.textContent.trim(),
          whole: !!name && name.scrollWidth <= name.clientWidth + 1,
          under: !!name && !!state && state.getBoundingClientRect().top >= name.getBoundingClientRect().bottom - 1,
        };
      });
      check(`68 ${theme} ${tab}: the open item's row is marked current`, current.count === 1, current.name ?? "");
      check(`68 ${theme} ${tab}: its name is whole, with its state under it`, current.whole && current.under);
      await shoot(page, `68-${tab}-${theme}`);
    });
  }
}

await browser.close();
clearTimeout(watchdog);
await writeFile(join(output, "results.json"), JSON.stringify(results, null, 2));
const failed = results.checks.filter((c) => !c.ok);
console.log(failed.length ? `FAIL settings audit: ${failed.length} of ${results.checks.length} checks failed` : `PASS settings audit: ${results.checks.length} checks`);
process.exit(failed.length ? 1 : 0);
