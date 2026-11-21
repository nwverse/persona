// Start web/e2e/changes-review-hub.py and a Vite server pointing at that loopback hub.
import assert from "node:assert/strict";
import { mkdir, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { chromium } from "playwright-core";

const [address, evidence, baseline] = process.argv.slice(2);
const origin = new URL(address);
assert.ok(["127.0.0.1", "localhost"].includes(origin.hostname));
const output = resolve(evidence);
await mkdir(output, { recursive: true });
const executable = await chromium.launch({ channel: "chrome", headless: true });
const results = [];
const cwd = "/Users/me/code/weather";
const file = cwd + "/src/forecast/rain/window.ts";
const second = cwd + "/tests/forecast/rain-window.test.ts";
const session = "/s/macbook/a1f3c2d4-0000-4000-8000-000000005157?token=mock";
const mark = (path) => '[data-review-file="' + path + '"]';

try {
  for (const mobile of baseline ? [false] : [false, true]) for (const theme of baseline ? ["light"] : ["light", "dark"]) for (const size of baseline ? ["standard"] : ["standard", "large"]) {
    const context = await executable.newContext({ viewport: mobile ? { width: 390, height: 844 } : { width: 1440, height: 960 },
      locale: "en-US", colorScheme: theme, hasTouch: mobile, isMobile: mobile });
    const name = baseline ? "before-desktop-light" : (mobile ? "mobile-" : "desktop-") + theme + (size === "large" ? "-large" : "");
    try {
      await context.addInitScript(({ theme, size }) => {
        localStorage.setItem("persona.theme", theme);
        localStorage.setItem("persona.language", "en");
        localStorage.setItem("persona.panelWidth", "870");
        localStorage.setItem("persona.sidebarCollapsed", "1");
        localStorage.setItem("persona.text-size", size);
      }, { theme, size });
      const page = await context.newPage();
      page.setDefaultTimeout(18_000);
      const errors = [], seen = [];
      page.on("pageerror", (error) => errors.push(error.message));
      page.on("response", (response) => {
        const url = new URL(response.url());
        if (/git\/(blob|review)$/.test(url.pathname)) seen.push([url.pathname, url.searchParams.get("path"), url.searchParams.get("rev"), response.status()]);
      });
      await page.goto(new URL(session, origin).href);
      const panel = page.locator("[data-changes-panel]");
      if (baseline) {
        const group = page.locator("[data-chat-scroll] button").filter({ hasText: "Worked for" }).last();
        await group.click();
        await page.locator("[data-action-row=Edit] > button").first().click();
        await panel.locator("[data-change-call]").first().waitFor();
        assert.equal(await panel.locator("[data-change-call]").count(), 2);
        assert.equal(await panel.locator("[data-net-diff]").count(), 0);
        await page.screenshot({ path: join(output, name + ".png") });
        results.push({ name, calls: 2, net: 0, errors });
        continue;
      }
      await page.locator("[data-open-changes]").last().click();
      const source = panel.locator(mark(file)), test = panel.locator(mark(second));
      const net = source.locator("[data-net-diff]");
      await source.locator('[data-net-diff][data-source="git"]').waitFor();
      await test.locator('[data-net-diff][data-source="git"]').waitFor();
      assert.equal(await panel.locator("[data-review-file]").count(), 2);
      assert.equal(await panel.locator("[data-change-call]").count(), 0);
      assert.ok(!(await net.textContent()).includes("start + 12"));
      assert.ok(!(await test.textContent()).includes("unrelated working-tree"));
      assert.ok((await source.textContent()).includes("start + defaultWindow"));
      assert.ok((await source.textContent()).includes("start + 6"));
      assert.equal(await source.locator('[data-side="new"][data-line="10"]').count(), 1);
      assert.equal(await source.locator('[data-side="old"][data-line="10"]').count(), 1);
      assert.ok(seen.some(([p,,rev,s]) => p.endsWith("/git/blob") && /^[0-9a-f]{40}$/.test(rev) && s === 200));
      if (!mobile) {
        await panel.locator('[data-diff-mode="split"]').click();
        assert.ok(await source.locator('[data-layout="split"]').count());
        await panel.locator('[data-diff-mode="unified"]').focus();
        await page.keyboard.press("Enter");
        assert.ok(await source.locator('[data-layout="unified"]').count());
      } else {
        assert.equal(await panel.locator('[data-diff-mode="split"]').count(), 0);
        const files = panel.getByRole("button", { name: /Edited files/ });
        await files.click();
        const search = panel.getByRole("searchbox");
        await search.fill("rain-window");
        assert.equal(await panel.locator("nav [data-change-file]").count(), 1);
        await panel.locator("nav [data-change-file]").click();
        assert.equal(await files.getAttribute("aria-expanded"), "false");
        await panel.getByRole("button", { name: "Previous file", exact: true }).click();
      }
      const options = panel.getByRole("button", { name: "Display options" });
      await options.click();
      const lines = panel.getByRole("combobox", { name: "Context lines" });
      await lines.selectOption("0");
      assert.equal(await source.locator('[data-side="new"][data-line="8"]').count(), 0);
      await lines.selectOption("10");
      assert.equal(await source.locator('[data-side="new"][data-line="2"]').count(), 1);
      await lines.selectOption("3");
      const whitespace = panel.getByRole("checkbox", { name: "Ignore whitespace" });
      await whitespace.check();
      await whitespace.uncheck();
      await options.click();
      await panel.locator("[data-wrap-toggle]").focus();
      await page.keyboard.press("Space");
      assert.equal(await panel.locator("[data-wrap-toggle]").getAttribute("aria-pressed"), "true");
      await source.locator("[data-history-toggle]").click();
      assert.equal(await source.locator("[data-change-call]").count(), 2);
      assert.ok((await source.textContent()).includes("start + 12"));
      await source.locator("[data-history-toggle]").click();
      await source.locator("[data-viewed]").check();
      assert.equal(await net.count(), 0);
      assert.ok((await panel.locator("[data-viewed-count]").textContent()).includes("1/2"));
      await source.locator("[data-viewed]").uncheck();
      const plus = source.locator('[data-side="old"][data-line="10"]');
      await plus.focus();
      await page.keyboard.press("Enter");
      await source.locator("[data-line-box] textarea").fill("Keep the previous window in the migration note.");
      await page.keyboard.press("Control+Enter");
      const plusNew = source.locator('[data-side="new"][data-line="10"]');
      await plusNew.click();
      await source.locator("[data-line-box] textarea").fill("Document the full-day window.");
      await page.keyboard.press("Control+Enter");
      const saved = await page.evaluate(() => Object.values(localStorage).filter((v) => String(v).includes("Document the full-day window.")).map((v) => JSON.parse(v)));
      assert.ok(saved.length && JSON.stringify(saved).includes('"scope":"exchange"'));
      assert.ok(JSON.stringify(saved).includes('"old":true') && JSON.stringify(saved).includes('"from":10'));
      await panel.locator('[data-change-scope="session"]').click();
      await panel.locator(mark(cwd + "/docs/forecast.md")).waitFor();
      assert.equal(await panel.locator("[data-review-file]").count(), 3);
      await panel.locator('[data-change-scope="exchange"]').click();
      await source.locator('[data-net-diff][data-source="git"]').waitFor();
      assert.equal(await panel.locator("[data-review-file]").count(), 2);
      if (!mobile && theme === "dark") await panel.locator('[data-diff-mode="split"]').click();
      await test.locator("[data-viewed]").check();
      await panel.locator("[data-change-diff]").evaluate((el) => { el.scrollTop = 0; });
      await page.mouse.move(10, 10);
      const box = await panel.boundingBox();
      const geometry = await panel.evaluate((p) => ({
        fit: p.scrollWidth <= p.clientWidth + 1,
        height: p.querySelector("[data-change-diff]").clientHeight,
        scale: getComputedStyle(document.documentElement).getPropertyValue("--text-scale").trim(),
        styledWords: p.querySelectorAll('[class*="bg-diff-add-word"], [class*="bg-diff-del-word"]').length,
        syntax: p.querySelectorAll('[class*="hljs-"]').length,
        codeOverflow: [...p.querySelectorAll("[data-layout]")].some((c) => c.scrollWidth > c.clientWidth + 1),
      }));
      assert.ok(geometry.fit && geometry.height > 240 && !geometry.codeOverflow, JSON.stringify(geometry));
      assert.ok(geometry.styledWords > 0 && geometry.syntax > 0);
      if (size === "large") assert.match(geometry.scale, /14\s*\/\s*13/);
      if (mobile) {
        for (const side of ["old", "new"]) {
          const hit = await source.locator('[data-side="' + side + '"][data-line="10"]').boundingBox();
          assert.ok(hit.width >= 44 && hit.height >= 44, JSON.stringify(hit));
        }
      }
      assert.deepEqual(errors, []);
      await page.getByText(/Comment [12] added above the message box/).first().waitFor({ state: "hidden", timeout: 8000 });
      await page.screenshot({ path: join(output, name + ".png") });
      await source.getByRole("button", { name: "Show more context" }).last().click();
      assert.ok((await net.textContent()).includes("windowLabel"));
      // A quoted source line goes through the draft card's public action and returns to its original side.
      if (!mobile) {
        await panel.locator('[data-change-scope="session"]').click();
        await page.locator('[data-comment-card="1"] button').first().click();
        assert.equal(await panel.locator('[data-change-scope="exchange"]').getAttribute("aria-pressed"), "true");
        await source.locator('[data-side="old"][data-line="10"]').waitFor();
      }
      if (!mobile && theme === "light" && size === "standard") {
        await plusNew.click();
        await page.screenshot({ path: join(output, "desktop-comment.png") });
      }
      results.push({ name, viewport: page.viewportSize(), panel: box, geometry, immutableBlobs: seen.filter(([p]) => p.endsWith("/git/blob")).length, errors });
    } catch (error) {
      const last = context.pages().at(-1);
      if (last) {
        await last.screenshot({ path: join(output, name + "-debug.png") });
        await writeFile(join(output, name + "-debug.txt"), await last.locator("body").innerText());
      }
      throw error;
    } finally { await context.close(); }
  }
  await writeFile(join(output, baseline ? "before-browser.json" : "browser.json"), JSON.stringify(results, null, 2) + "\n");
  console.log(JSON.stringify(results));
} finally { await executable.close(); }
