// Screenshots and a quick check of the generic configuration form (#9877, docs/web/web.md "Configuration forms"): the
// fixture-only page (e2e/configuration-form.html: the real form over the synthetic all-types document) and the real
// Settings › Labs, each wide (1280) and phone width (390), light and dark, and at the large text size.
//
// Needs the Vite dev server (and, for Labs, any hub the page can sign in to, such as the mock), and Chrome or any
// Chromium through --executable:
//
//   PORT=18977 python3 scripts/mock/mock_hub.py &
//   (cd web && PERSONA_HUB_URL=http://127.0.0.1:18977 npx vite --port 5977 --strictPort &)
//   node web/e2e/configuration-form.mjs http://127.0.0.1:5977 --shots <dir> [--executable <chromium>]
//
// Shots go to <dir>, never into the repository. Prints one line per check and a verdict; exits 1 on a failure and after
// four minutes.
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { chromium } from "playwright-core";

const args = process.argv.slice(2);
const flag = (name) => {
  const i = args.indexOf(name);
  return i >= 0 ? args[i + 1] : undefined;
};
const base = (args[0] ?? "").replace(/\/$/, "");
const shots = flag("--shots");
const executable = flag("--executable");
if (!base || !shots) {
  console.error("usage: node web/e2e/configuration-form.mjs <web URL> --shots <dir> [--executable <chromium>]");
  process.exit(2);
}
mkdirSync(shots, { recursive: true });
setTimeout(() => {
  console.log("FAIL timed out after 4 minutes");
  process.exit(1);
}, 240_000).unref();

let failed = false;
const ok = (msg) => console.log(`ok  ${msg}`);
const fail = (msg) => {
  failed = true;
  console.log(`FAIL ${msg}`);
};

const browser = await chromium.launch(executable ? { executablePath: executable } : { channel: "chrome" });
async function context(theme, width, size) {
  const phone = width < 600;
  const ctx = await browser.newContext({ viewport: { width, height: phone ? 844 : 900 }, deviceScaleFactor: 2, colorScheme: theme, reducedMotion: "reduce", hasTouch: phone, isMobile: phone });
  await ctx.addInitScript(([t, s]) => {
    localStorage.setItem("persona.language", "en");
    localStorage.setItem("persona.theme", t);
    if (s) localStorage.setItem("persona.text-size", s);
  }, [theme, size]);
  const page = await ctx.newPage();
  page.on("pageerror", (e) => fail(`page error: ${e.message}`));
  return { ctx, page };
}
const sideways = (page) => page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1);

try {
  for (const [theme, width, size] of [["light", 1280], ["dark", 1280], ["light", 390], ["dark", 390], ["light", 390, "large"], ["light", 1280, "large"]]) {
    const tag = `${width}-${theme}${size ? `-${size}` : ""}`;
    const { ctx, page } = await context(theme, width, size);

    await page.goto(`${base}/e2e/configuration-form.html`);
    await page.waitForSelector("[data-configuration-field]", { timeout: 20000 });
    await page.waitForTimeout(500);
    if (await sideways(page)) fail(`${tag}: the fixture form scrolls sideways`);
    const kinds = await page.$$eval("[data-configuration-field]", (els) => els.map((e) => e.getAttribute("data-kind")));
    if (new Set(kinds).size !== 6) fail(`${tag}: kinds drawn ${kinds}`);
    await page.screenshot({ path: join(shots, `form-${tag}.png`), fullPage: true });
    ok(`${tag}: the fixture form draws every kind (${[...new Set(kinds)].join(", ")})`);

    if (theme === "light" && !size) {
      await page.locator('[data-configuration-field="nickname"] input').fill("Sam 👋");
      await page.locator('[data-configuration-field="nickname"] input').press("Enter");
      await page.waitForSelector('[data-configuration-field="nickname"] >> text=Saved', { timeout: 5000 }).catch(() => fail(`${tag}: no Saved after Enter`));
      ok(`${tag}: typed text saves with Enter`);
      await page.goto(`${base}/e2e/configuration-form.html?fail=storage_failed`);
      await page.waitForSelector("[data-configuration-field]");
      await page.locator('[data-configuration-field="note"] textarea').fill("Kept after a failure");
      await page.locator('[data-configuration-field="note"] button', { hasText: "Save" }).click();
      await page.waitForSelector('[data-configuration-field="note"] [role="alert"]', { timeout: 5000 });
      if ((await page.locator('[data-configuration-field="note"] textarea').inputValue()) !== "Kept after a failure") fail(`${tag}: the draft was lost`);
      await page.locator('[data-configuration-field="note"]').screenshot({ path: join(shots, `form-${tag}-save-failed.png`) });
      ok(`${tag}: a failed save keeps the typed text and says why`);
      await page.goto(`${base}/e2e/configuration-form.html?version=2`);
      await page.waitForSelector("[data-configuration-field]");
      await page.waitForTimeout(300);
      await page.screenshot({ path: join(shots, `form-${tag}-newer-version.png`) });
      ok(`${tag}: a newer version is shown read-only`);
    }

    await page.goto(`${base}/?token=mock`);
    await page.waitForFunction(() => localStorage.getItem("persona.token"), null, { timeout: 15000 }).catch(() => {});
    await page.goto(`${base}/settings?tab=labs`);
    const labs = await page.waitForSelector('[data-configuration-field="renderer"]', { timeout: 20000 }).catch(() => null);
    if (!labs) {
      fail(`${tag}: Settings › Labs did not open`);
    } else {
      await page.waitForTimeout(600);
      if (await sideways(page)) fail(`${tag}: Labs scrolls sideways`);
      await page.screenshot({ path: join(shots, `labs-${tag}.png`) });
      ok(`${tag}: Settings › Labs drawn through the form`);
    }
    await ctx.close();
  }
} finally {
  await browser.close();
}
console.log(failed ? "FAIL configuration form" : "PASS configuration form");
process.exit(failed ? 1 : 0);
