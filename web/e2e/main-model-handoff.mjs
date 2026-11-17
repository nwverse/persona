// Real Chromium UI with synthetic hub data and a controlled handoff response (#10032).
// MOCK_MAIN_ACTIONS=1 MOCK_MODEL_PICK=otherkind PORT=18897 python3 scripts/mock/mock_hub.py
// cd web && PERSONA_HUB_URL=http://127.0.0.1:18897 npx vite --port 5387 --strictPort
// node web/e2e/main-model-handoff.mjs http://127.0.0.1:5387 --shots <dir> --chromium <executable>
// Server/node tests cover preparation and commit; this fixture checks feedback, failure recovery and navigation.
import assert from "node:assert/strict";
import { mkdirSync } from "node:fs";
import { join, resolve } from "node:path";
import { chromium } from "playwright-core";

const args = process.argv.slice(2);
const flag = (name) => { const at = args.indexOf(name); return at < 0 ? undefined : args[at + 1]; };
const base = args[0];
assert.ok(["localhost", "127.0.0.1", "[::1]"].includes(new URL(base).hostname), "Synthetic fixture must use loopback");
const shots = resolve(flag("--shots") ?? "/tmp/persona-main-model-handoff");
mkdirSync(shots, { recursive: true });
const source = "b2e4d6f8-0000-4000-8000-000000000001";
const target = "b2e4d6f8-0000-4000-8000-000000000099";
const browser = await chromium.launch({ executablePath: flag("--chromium") || process.env.PERSONA_E2E_CHROMIUM, headless: true, args: ["--no-sandbox"] });
const watchdog = setTimeout(() => { console.error("TIMEOUT main-model handoff"); process.exit(124); }, 180_000);
const timings = [];
try {
  for (const theme of ["light", "dark"]) {
    for (const phone of [false, true]) {
      const tag = `${theme}-${phone ? "phone-large" : "desktop"}`;
      const context = await browser.newContext({ viewport: phone ? { width: 390, height: 844 } : { width: 1280, height: 900 }, colorScheme: theme, locale: "en-US", reducedMotion: "reduce", hasTouch: phone, isMobile: phone });
      await context.addInitScript(({ theme, phone }) => {
        localStorage.setItem("persona.theme", theme);
        localStorage.setItem("persona.language", "en");
        localStorage.setItem("persona.text-size", phone ? "large" : "standard");
      }, { theme, phone });
      const page = await context.newPage();
      const errors = [];
      page.on("pageerror", (e) => errors.push(String(e)));
      let replacement;
      // Advertise only the capability under test; no live provider is called.
      await page.routeWebSocket("**/ws*", (socket) => {
        const server = socket.connectToServer();
        server.onMessage((message) => {
          try {
            const body = JSON.parse(String(message));
            if (Array.isArray(body.features)) body.features.push("assistant_main_model_handoff");
            socket.send(JSON.stringify(body));
          } catch { socket.send(message); }
        });
        socket.onMessage((message) => server.send(message));
      });
      await page.route("**/api/v1/assistant", async (route) => {
        const response = await route.fetch();
        const body = await response.json();
        if (replacement) {
          body.main = target;
          body.main_machine_id = replacement.machine_id;
          body.conversations = [...(body.conversations ?? []), replacement];
        }
        await route.fulfill({ response, json: body });
      });
      let finish;
      let requests = 0;
      let requestedAt;
      await page.route("**/api/v1/assistant/main/model", async (route) => {
        requests++;
        requestedAt = Date.now();
        assert.equal(route.request().postDataJSON().expect, source);
        await new Promise((resolve) => { finish = resolve; });
        if (requests === 1) {
          await route.fulfill({ status: 503, json: { error: "Target unavailable; current main chat kept" } });
        } else {
          replacement = { session_id: target, machine_id: "server", assistant: true, title: "Main chat", cwd: "/home/me/.persona/assistant", status: "idle", state: "idle", agent: "claude", launcher: "claude", model: "opus", model_pick: { launcher: "claude", model: "opus" } };
          await route.fulfill({ json: { session: replacement, replaced: source, carried: true } });
        }
      });
      await page.goto(`${base}/persona/${source}?token=mock`);
      await page.locator("[data-model-pill]").first().waitFor();
      const draft = page.locator("textarea").last();
      await draft.fill("Keep this unsent message");
      await page.locator("[data-model-pill]").first().click();
      const panel = page.locator("[data-model-panel]");
      await panel.waitFor();
      await panel.locator("[data-panel-row]", { hasText: "Opus 5.5" }).first().getByText("Opus 5.5", { exact: true }).click();
      const pending = panel.getByRole("status").filter({ hasText: "Switching models" });
      await pending.waitFor({timeout: 8000}).catch(async (error) => { console.error(tag, {requests, pill: await page.locator("[data-model-pill]").first().getAttribute("aria-label"), panel: await panel.innerText()}); await page.screenshot({path:join(shots, `${tag}-debug.png`)}); throw error; });
      assert.match(await pending.innerText(), /Switching models/);
      assert.equal(requests, 1);
      const feedbackMs = Date.now() - requestedAt;
      assert.ok(feedbackMs < 1000, `${tag}: pending feedback within one second`);
      await page.screenshot({ path: join(shots, `${tag}-pending.png`) });
      finish();
      await page.getByText("Target unavailable; current main chat kept", { exact: false }).last().waitFor();
      assert.equal(await draft.inputValue(), "Keep this unsent message");
      assert.ok(page.url().includes(source));
      assert.equal(requests, 1, "failure is not automatically retried");
      await page.screenshot({ path: join(shots, `${tag}-failure.png`) });
      await panel.locator("[data-panel-row]", { hasText: "Opus 5.5" }).first().getByText("Opus 5.5", { exact: true }).click();
      await pending.waitFor();
      finish();
      await page.waitForFunction(() => location.pathname === "/");
      await panel.waitFor({ state: "hidden" });
      await page.screenshot({ path: join(shots, `${tag}-success.png`) });
      assert.equal(requests, 2);
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, `${tag}: no overflow`);
      assert.deepEqual(errors, []);
      timings.push({ view: tag, feedbackMs });
      console.log(`PASS ${tag}: pending, draft recovery, one explicit retry, confirmed navigation (${feedbackMs} ms feedback)`);
      await context.close();
    }
  }
  console.log(JSON.stringify({ timings, screenshots: shots }));
} finally {
  clearTimeout(watchdog);
  await browser.close();
}
