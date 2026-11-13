// Real isolated manager/PostgreSQL fixture; synthetic repository and computer data.
// node web/e2e/workspace-naming.mjs <loopback web origin> <shots directory> <Chromium executable>
import assert from "node:assert/strict";
import { mkdir, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { chromium } from "playwright-core";

const [origin, shots, executablePath] = process.argv.slice(2);
assert(/^http:\/\/(127\.0\.0\.1|localhost):\d+$/.test(origin), "Use an isolated loopback origin");
assert(shots && executablePath, "Provide screenshot output and the installed browser");
await mkdir(shots, { recursive: true });
const deadline = setTimeout(() => { console.error("FAIL workspace naming browser deadline"); process.exit(124); }, 240_000);
const browser = await chromium.launch({ executablePath, env: { ...process.env, TMPDIR: process.env.WORKSPACE_NAMING_BROWSER_TMP || process.env.TMPDIR }, args: ["--no-sandbox", "--disable-dev-shm-usage"] });
const errors = [];
let activePage;
try {
  for (const scheme of ["light", "dark"]) {
    for (const width of [1440, 390]) {
      const context = await browser.newContext({ viewport: { width, height: 1600 }, colorScheme: scheme, locale: "en-US", reducedMotion: "reduce", ...(width === 390 ? { isMobile: true, hasTouch: true } : {}) });
      await context.addInitScript(({ scheme }) => { localStorage.setItem("persona.token", "synthetic-naming-token"); localStorage.setItem("persona.theme", scheme); localStorage.setItem("persona.language", "en"); }, { scheme });
      await context.route("**/*", (route) => new URL(route.request().url()).origin === origin ? route.continue() : route.abort());
      const page = await context.newPage(); activePage = page; page.on("pageerror", (error) => errors.push(error.message));
      await page.goto(origin + "/settings?tab=persona");
      const group = page.locator('[data-settings-group="workspace-naming"]');
      await group.getByRole("button", { name: "Workspace naming", exact: true }).click();
      const task = group.getByLabel("Task folder template", { exact: true });
      await task.waitFor();
      await task.fill("{yyyy}{mmdd}/task-{task-id}-{task-slug}");
      if (await group.getByRole("button", { name: "Save", exact: true }).isEnabled()) await group.getByRole("button", { name: "Save", exact: true }).click();
      await page.waitForFunction(() => [...document.querySelectorAll('[data-settings-group="workspace-naming"] button')].some((b) => b.textContent.trim() === "Save" && b.disabled));
      await page.reload();
      await group.getByRole("button", { name: "Workspace naming", exact: true }).click();
      await task.waitFor(); assert.equal(await task.inputValue(), "{yyyy}{mmdd}/task-{task-id}-{task-slug}", "settings persisted through reload");
      await group.getByRole("button", { name: "Preview naming", exact: true }).click();
      await group.locator("[data-naming-root]").waitFor();
      assert.match(await group.locator("[data-naming-root]").innerText(), /^\/home\/example\/workspace\/\d{8}\/task-todo_example-example-task$/);
      await group.getByLabel("Folder layout", { exact: true }).selectOption("root");
      await group.getByRole("button", { name: "Preview naming", exact: true }).click();
      await group.locator("[data-naming-root]").waitFor();
      assert.equal(await group.locator("dt").first().innerText(), ".", "root layout uses internal dot mapping");
      await group.scrollIntoViewIfNeeded();
      assert(await group.evaluate((el) => el.scrollWidth <= el.clientWidth), "no horizontal overflow");
      await group.screenshot({ type: "jpeg", quality: 80, path: join(shots, `settings-${scheme}-${width}.jpg`) });
      await task.fill("../unsafe");
      await group.getByRole("button", { name: "Preview naming", exact: true }).click();
      await group.getByRole("alert").waitFor();
      assert.equal(await task.inputValue(), "../unsafe", "failed preview preserves draft");
      await group.screenshot({ type: "jpeg", quality: 80, path: join(shots, `invalid-${scheme}-${width}.jpg`) });

      // Direct API state is supplied by the real fixture, not browser interception.
      const todo = await (await page.request.get(origin + "/api/v1/_qa/todo")).json();
      await page.goto(origin + "/todos?id=" + encodeURIComponent(todo.id));
      await page.getByRole("button", { name: "Workspace naming", exact: true }).click();
      const field = page.getByLabel("Task branch template", { exact: true }); await field.waitFor();
      await field.fill("custom/{task-id}/{repo}");
      // Scope to the advanced editor so unrelated Todo buttons cannot save instead.
      const editor = page.getByRole("button", { name: "Workspace naming", exact: true }).locator("..");
      if (await editor.getByRole("button", { name: "Save", exact: true }).isEnabled()) await editor.getByRole("button", { name: "Save", exact: true }).click();
      await page.waitForFunction(() => [...document.querySelectorAll("input")].some((el) => el.value === "custom/{task-id}/{repo}" && !document.body.textContent.includes("Unsaved changes")));
      await page.reload(); await page.getByRole("button", { name: "Workspace naming", exact: true }).click();
      await field.waitFor(); assert.equal(await field.inputValue(), "custom/{task-id}/{repo}", "task override persisted");
      await editor.getByRole("button", { name: "Preview naming", exact: true }).click();
      await editor.locator("[data-naming-root]").waitFor();
      assert.equal(await editor.locator("dl").count(), 2, "both repository names previewed");
      if (width === 390) await page.evaluate(() => document.documentElement.style.setProperty("--text-scale", "2"));
      assert(await editor.evaluate((el) => el.scrollWidth <= el.clientWidth), "task editor fits at enlarged text");
      if (width === 390) {
        await editor.getByRole("button", { name: "Workspace naming", exact: true }).scrollIntoViewIfNeeded();
        await page.screenshot({ type: "jpeg", quality: 80, path: join(shots, `task-${scheme}-${width}-controls.jpg`) });
        await editor.locator("[data-naming-root]").scrollIntoViewIfNeeded();
        await page.screenshot({ type: "jpeg", quality: 80, path: join(shots, `task-${scheme}-${width}-preview.jpg`) });
      } else await editor.screenshot({ type: "jpeg", quality: 80, path: join(shots, `task-${scheme}-${width}.jpg`) });
      await context.close();
      console.log(`PASS workspace naming ${scheme} ${width}px: real settings/override reload, root/container preview, invalid draft, overflow`);
    }
  }
  assert.deepEqual(errors, [], "no browser page errors");
  console.log("PASS isolated workspace naming browser journey");
  await fetch(origin + "/api/v1/_qa/stop");
  await new Promise((resolve) => setTimeout(resolve, 500));
} catch (error) {
  if (activePage && !activePage.isClosed()) { await activePage.screenshot({ type: "jpeg", quality: 80, path: join(shots, "failure.jpg"), fullPage: true }); await writeFile(join(shots, "failure.txt"), await activePage.locator("body").innerText()); }
  throw error;
} finally { clearTimeout(deadline); await browser.close(); }
