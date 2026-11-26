// Review captures for the Machines overview and cross-tool selection. Run against the loopback mock hub
// with MOCK_MANAGERS=0, two online computers named Computer A and Computer B, and a waiting session.
// node web/e2e/machine-tool-context.mjs <local web URL> <output directory> (CHROME_BIN overrides Chrome).
import assert from "node:assert/strict";
import { mkdir } from "node:fs/promises";
import { join } from "node:path";
import { chromium } from "playwright-core";

const [url, out] = process.argv.slice(2);
if (!out || !["localhost", "127.0.0.1", "[::1]"].includes(new URL(url).hostname)) {
  throw new Error("Supply the local mock web URL and a screenshot directory outside the repository.");
}
await mkdir(out, { recursive: true });
const browser = await chromium.launch(process.env.CHROME_BIN
  ? { executablePath: process.env.CHROME_BIN, args: ["--no-sandbox"] }
  : { channel: "chrome" });
const timeout = setTimeout(() => { console.error("TIMEOUT: Machines browser verification"); process.exit(2); }, 180_000);

async function verify(width, theme, textSize) {
  const height = width === 390 ? 844 : 900;
  const context = await browser.newContext({ viewport: { width, height }, hasTouch: width === 390, colorScheme: theme });
  try {
    await context.addInitScript(({ theme, textSize }) => {
      localStorage.setItem("persona.theme", theme);
      localStorage.setItem("persona.language", "en");
      localStorage.setItem("persona.text-size", textSize);
    }, { theme, textSize });
    const page = await context.newPage();
    const errors = [];
    page.on("pageerror", (e) => errors.push(e.message));
    await page.route("**/machines/*/services", (r) => r.fulfill({ json: {
      os: "linux", managers: [{ kind: "systemd", available: true }], containers: [],
      services: [{ id: "example", name: "Example service", kind: "systemd", state: "running", description: "Background synchronization" }],
    } }));
    await page.route("**/machines/*/network", (r) => r.fulfill({ json: {
      interfaces: [{ name: "Ethernet", up: true, addresses: [] }], proxy: { active: false },
      probes: [{ name: "Hub connection", target: "example.com", protocol: "https", reachable: true, latency_ms: 24, status_code: 200 }],
    } }));
    await page.goto(new URL("/settings?token=mock", url).href);
    await page.waitForSelector("aside, nav");
    await page.goto(new URL("/machines", url).href);

    const views = page.getByRole("radiogroup", { name: "View", exact: true });
    const choices = views.getByRole("radio");
    const tokens = page.getByTestId("machines-tokens");
    const picker = page.getByTestId("machine-picker");
    const capture = async (view) => {
      await page.evaluate(() => document.fonts.ready);
      const fits = await views.evaluate((el) => el.scrollWidth <= el.clientWidth + 1 && el.getBoundingClientRect().right <= innerWidth + 1);
      assert(fits, "View controls must fit the available width");
      await page.screenshot({ path: join(out, `${width}-${theme}-${textSize}-${view}.png`), animations: "disabled" });
    };

    assert.equal(await choices.first().innerText(), "Cards");
    assert.equal(await choices.last().innerText(), "Assistant resources");
    await page.getByRole("link", { name: /\d+ needs? you/i }).first().waitFor();
    await tokens.waitFor();
    const titleFits = await page.getByRole("heading", { name: "Computer A", exact: true })
      .evaluate((el) => el.scrollWidth <= el.clientWidth + 1);
    assert(titleFits, "The waiting link must leave room for the computer name");
    await capture("cards");
    await views.getByRole("radio", { name: "Services", exact: true }).click();
    await picker.selectOption({ label: "Computer B" });
    await page.getByText("Example service", { exact: true }).waitFor();
    assert.equal(await picker.locator("option:checked").innerText(), "Computer B");
    assert.equal(await tokens.count(), 0);
    await capture("services");
    await views.getByRole("radio", { name: "Network", exact: true }).click();
    await page.getByText("Hub connection", { exact: true }).waitFor();
    assert.equal(await picker.locator("option:checked").innerText(), "Computer B");
    assert.equal(await tokens.count(), 0);
    await capture("network");
    assert.deepEqual(errors, []);
    console.log(`PASS ${width}×${height} ${theme} ${textSize}: Cards, Services and Network`);
  } finally {
    await context.close();
  }
}
try {
  for (const theme of ["light", "dark"]) {
    for (const width of [1440, 390]) {
      for (const textSize of ["standard", "large"]) await verify(width, theme, textSize);
    }
  }
  console.log("PASS: Machines browser verification");
} finally {
  await browser.close();
  clearTimeout(timeout);
}
