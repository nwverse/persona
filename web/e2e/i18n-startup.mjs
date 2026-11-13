// Browser check of the split client catalog (docs/web/web.md "Internationalization"): the built page starts on the
// first screen's part of the catalog and loads the rest after it, and every lazy import waits for the rest. With the
// rest held back, cold loads of the first screen and of lazy pages, in each language, must never show a catalog key in
// place of its text, and the first screen must draw before the rest arrives.
//
// Needs the built page served by `vite preview` (it proxies /api like the dev server) against the fake hub, and Chrome
// (playwright-core drives the installed one, or PERSONA_CHROME). Use unused loopback ports:
//
//   PORT=<hub port> python3 scripts/mock/mock_hub.py &
//   (cd web && npm run build && PERSONA_HUB_URL=http://127.0.0.1:<hub port> npx vite preview --port <web port> --strictPort &)
//   node web/e2e/i18n-startup.mjs http://127.0.0.1:<web port> [--hold <ms>]
//
// The runner starts and stops neither. Prints one line per language and page and a verdict; exits 1 on a failure.
import { readdirSync, readFileSync } from "node:fs";
import { chromium } from "playwright-core";

const args = process.argv.slice(2);
const base = new URL(args[0] ?? "http://invalid");
const holdAt = args.indexOf("--hold");
const HOLD_MS = holdAt >= 0 ? Number(args[holdAt + 1]) : 3000;
if (!["127.0.0.1", "localhost", "[::1]"].includes(base.hostname) || base.pathname !== "/" || !(HOLD_MS >= 0)) {
  console.error("usage: node web/e2e/i18n-startup.mjs <loopback web origin> [--hold <ms>]");
  process.exit(2);
}
const LANGS = ["en", "zh-Hans", "ja"];
// The first screen, then lazy pages opened cold (one path segment: the build's files are relative to the page).
const PAGES = ["/", "/agents", "/settings", "/machines", "/inbox", "/todos", "/health"];

// Every client key, and each plural key's base: t() hands back the base when nothing answers it.
const locales = new URL("../src/i18n/locales/en/", import.meta.url);
const keys = new Set();
for (const file of readdirSync(locales).filter((n) => n.endsWith(".json") && !n.startsWith("_") && !n.startsWith("admin-"))) {
  for (const name of Object.keys(JSON.parse(readFileSync(new URL(file, locales), "utf8")))) {
    keys.add(`${file.slice(0, -5)}.${name}`);
    keys.add(`${file.slice(0, -5)}.${name.replace(/_(zero|one|two|few|many|other)$/, "")}`);
  }
}

// The startup catalogs are the ones index.html preloads; any other client catalog is a rest one.
const html = await (await fetch(base)).text();
const startup = new Set(Object.values(JSON.parse(/"catalogs":(\{[^}]*\})/.exec(html)?.[1] ?? "{}")));
if (!startup.size) {
  console.error("no startup catalogs in index.html: is this the built page (vite preview), not the dev server?");
  process.exit(2);
}

// Records every catalog key that ever shows, in text or in the attributes people read, from the first node on.
function watch(keyList) {
  const known = new Set(keyList);
  const seen = new Set();
  const attrs = ["title", "aria-label", "placeholder", "alt"];
  const scan = (text) => {
    for (const [word] of (text ?? "").matchAll(/[a-z0-9][a-z0-9-]*\.[A-Za-z0-9_]+/g)) if (known.has(word)) seen.add(word);
  };
  const walk = (node) => {
    if (node.nodeType === Node.TEXT_NODE) return scan(node.data);
    if (node.nodeType !== Node.ELEMENT_NODE) return;
    for (const a of attrs) scan(node.getAttribute(a));
    node.childNodes.forEach(walk);
  };
  new MutationObserver((records) => {
    for (const r of records) {
      if (r.type === "characterData") scan(r.target.data);
      else if (r.type === "attributes") scan(r.target.getAttribute(r.attributeName));
      else r.addedNodes.forEach(walk);
    }
    scan(document.title);
  }).observe(document, { subtree: true, childList: true, characterData: true, attributes: true, attributeFilter: attrs });
  globalThis.__bareKeys = () => {
    walk(document.documentElement);
    return [...seen];
  };
}

const chrome = process.env.PERSONA_CHROME;
const browser = await chromium.launch(chrome ? { executablePath: chrome, headless: true } : { channel: "chrome", headless: true });
let failed = 0;
try {
  for (const lang of LANGS) {
    for (const path of PAGES) {
      const context = await browser.newContext({ viewport: { width: 1280, height: 860 } });
      await context.addInitScript((lang) => {
        localStorage.setItem("persona.token", "mock");
        localStorage.setItem("persona.language", lang);
      }, lang);
      await context.addInitScript(watch, [...keys]);
      const page = await context.newPage();
      const errors = [];
      page.on("pageerror", (e) => errors.push(e.message));
      let held = 0;
      let drawnFirst;
      let release;
      const released = new Promise((r) => (release = r));
      await context.route(/\/assets\/_meta\.json-[^/]+\.js$/, async (route) => {
        if (startup.has(new URL(route.request().url()).pathname.slice(1))) return route.continue();
        held++;
        await new Promise((r) => setTimeout(r, HOLD_MS));
        drawnFirst = await page.evaluate(() => (document.getElementById("root")?.innerText.trim().length ?? 0) > 0).catch(() => false);
        await route.continue();
        release();
      });
      const started = Date.now();
      await page.goto(new URL(path, base).href);
      await Promise.race([released, new Promise((r) => setTimeout(r, HOLD_MS + 30_000))]);
      await page.waitForFunction(() => typeof globalThis.__personaI18nRest === "function" && globalThis.__personaI18nRest() === undefined, null, { timeout: 30_000 });
      await page.waitForTimeout(1500);
      const bare = await page.evaluate(() => globalThis.__bareKeys());
      const text = await page.evaluate(() => document.getElementById("root")?.innerText.trim().length ?? 0);
      const problems = [
        ...((await page.evaluate(() => document.documentElement.lang)) === lang ? [] : [`the page did not run in ${lang}`]),
        ...(held === 1 ? [] : [`the rest catalog was fetched ${held} times`]),
        ...(path === "/" && !drawnFirst ? ["the first screen did not draw before the rest of the catalog arrived"] : []),
        ...(text > 0 ? [] : ["the page drew nothing"]),
        ...bare.map((k) => `bare key ${k}`),
        ...errors.map((e) => `page error: ${e}`),
      ];
      failed += problems.length ? 1 : 0;
      console.log(`${problems.length ? "FAIL" : "ok  "} ${lang.padEnd(7)} ${path.padEnd(9)} ${Date.now() - started} ms, first screen before the rest: ${drawnFirst}${problems.length ? `\n     ${problems.join("\n     ")}` : ""}`);
      await context.close();
    }
  }
} finally {
  await browser.close();
}
console.log(failed ? `FAIL: ${failed} of ${LANGS.length * PAGES.length} loads` : `PASS: ${LANGS.length * PAGES.length} loads, rest held ${HOLD_MS} ms, no bare key`);
process.exit(failed ? 1 : 0);
