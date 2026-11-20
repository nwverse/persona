// Real Agent → Shell → Agent row clicks against scripts/mock/mock_hub.py with MOCK_TERMINAL_{PANES,STREAM,CONTROL,FULL}=1.
// node web/e2e/live-tab-frame.mjs <local Web URL> <evidence directory>
import assert from "node:assert/strict";
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { chromium } from "playwright-core";

const [base = "http://127.0.0.1:5173", output = "work/live-tabs"] = process.argv.slice(2);
mkdirSync(output, { recursive: true });
const browser = await chromium.launch({ channel: "chrome" });
const cases = [];
try {
  for (const width of [390, 768, 1360]) for (const theme of ["light", "dark"]) for (const large of [false, true]) {
    const context = await browser.newContext({ viewport: { width, height: width === 390 ? 844 : 860 }, locale: "en-US", colorScheme: theme, reducedMotion: "reduce", hasTouch: width < 1024 });
    await context.addInitScript(({ scheme, large }) => {
      localStorage.setItem("persona.token", "mock");
      localStorage.setItem("persona.language", "en");
      localStorage.setItem("persona.theme", scheme);
      localStorage.setItem("persona.text-size", large ? "large" : "standard");
      localStorage.setItem("persona.agentGrouping", "machine");
      localStorage.setItem("sidebar.terminals", "1");
      localStorage.setItem("persona.terminal.defaultMode", "pane");
      localStorage.setItem("persona.terminal.fullHint", "1");
    }, { scheme: theme, large });
    const page = await context.newPage();
    const frames = [];
    page.on("websocket", (socket) => socket.on("framesent", ({ payload }) => {
      if (typeof payload === "string") frames.push(JSON.parse(payload).type);
    }));
    const header = page.locator("main [data-live-tab-header]");
    const toolbar = page.locator("[data-terminal-toolbar]:visible");
    const radio = (name) => page.getByRole("radio", { name, exact: true });
    const row = async (path) => {
      if (width < 1024) await page.locator("main header").getByRole("button", { name: /^Sidebar/ }).click();
      await page.locator(`[data-sidebar-rows] a[href="${path}"]`).click();
    };
    const fullFrame = async (kind) => {
      await toolbar.waitFor();
      await radio("Full view").click();
      const strip = page.locator('.xterm-rows:not([inert]) > div:visible').filter({ hasText: /1 main.*2 logs/ });
      await strip.first().waitFor();
      assert.equal(await strip.count(), 1);
      assert.equal(await page.locator("main header:visible").count(), 1);
      assert.equal(await page.locator("[data-terminals-list]:visible").count(), 0);
      assert.equal(await toolbar.count(), 1);
      await toolbar.getByRole("status").filter({ hasText: "Connected" }).waitFor();
      // A cached xterm can be reparented and fitted after its first connected frame, while the phone drawer closes.
      // Measure once the host's actual grid has reached the terminal viewport's content edge.
      await page.waitForFunction(() => {
        const shown = (el) => Boolean(el.offsetHeight && !el.closest("[inert]"));
        const bar = [...document.querySelectorAll("[data-terminal-toolbar]")].find(shown);
        const row = [...document.querySelectorAll(".xterm-rows > div")].find((el) => shown(el) && /1 main.*2 logs/.test(el.textContent));
        const viewport = row?.closest(".xterm")?.parentElement?.parentElement;
        if (!bar || !viewport) return false;
        const padding = getComputedStyle(viewport), rect = row.getBoundingClientRect(), toolbar = bar.getBoundingClientRect();
        return Math.abs(rect.x - toolbar.x - parseFloat(padding.paddingLeft)) < 0.5 &&
          Math.abs(rect.y - toolbar.bottom - parseFloat(padding.paddingTop)) < 0.5;
      });
      const box = async (locator) => {
        const { x, y, width, height } = await locator.boundingBox();
        return { x, y, width, height };
      };
      const geometry = { header: await box(header), toolbar: await box(toolbar), strip: await box(strip) };
      assert.equal(geometry.header.height, 56);
      assert.equal(geometry.toolbar.y, geometry.header.y + geometry.header.height);
      assert.equal(geometry.header.x, geometry.toolbar.x);
      assert.ok((await header.locator("h1").boundingBox()).width > 0);
      const heading = await header.locator("h1").innerText();
      const location = await header.locator("[data-tab-location]").innerText();
      if (kind !== "agent-return") await page.screenshot({ path: join(output, `${kind}-${width}-${theme}${large ? "-large" : ""}.png`) });
      cases.push({ width, theme, large, kind, heading, location, geometry, stripCount: 1 });
      return geometry;
    };

    await page.goto(base + "/terminal?view=terminals");
    await page.locator("[data-terminals-list] a").first().waitFor();
    const agentRow = "/t/server/w1/w1%3At1";
    await row(agentRow);
    await header.getByRole("radio", { name: "Conversation" }).waitFor();
    assert.equal(await radio("Conversation").getAttribute("aria-checked"), "true");
    assert.equal(await header.locator("h1").innerText(), "perf");
    assert.equal(await toolbar.count(), 0);
    assert.ok(!frames.some((type) => type === "terminal_subscribe" || type === "herdr_open"));
    await radio("Conversation").focus();
    await page.keyboard.press("ArrowRight");
    await radio("Single pane").waitFor();
    const agent = await fullFrame("agent");
    await row("/terminal/server/pane%3Aw1%3Ap2");
    await header.getByRole("heading", { name: "shell" }).waitFor();
    assert.equal(await header.getByRole("radiogroup").count(), 0);
    const shell = await fullFrame("shell");
    await row(agentRow);
    await header.getByRole("heading", { name: "perf" }).waitFor();
    const returned = await fullFrame("agent-return");
    for (const other of [shell, returned]) {
      assert.deepEqual(other.header, agent.header);
      assert.deepEqual(other.toolbar, agent.toolbar);
      assert.equal(other.strip.x, agent.strip.x);
      assert.equal(other.strip.y, agent.strip.y);
    }
    assert.ok(frames.includes("herdr_open"));
    await context.close();
  }
} finally {
  writeFileSync(join(output, "geometry.json"), JSON.stringify(cases, null, 2) + "\n");
  await browser.close();
}
console.log(`${cases.length} live frames verified in 12 Agent → Shell → Agent trips`);
