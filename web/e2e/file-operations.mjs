// Real-browser acceptance for remote copy / compress progress and Cancel (#6113).
// Start an exclusively owned mock and a Vite page backed by it:
//   MOCK_FILES=1 MOCK_FILE_OPS=1 MOCK_FILE_OP_DELAY=20 PORT=18961 python3 scripts/mock/mock_hub.py &
//   (cd web && PERSONA_HUB_URL=http://127.0.0.1:18961 npx vite --port 15961 --strictPort &)
//   node web/e2e/file-operations.mjs http://127.0.0.1:15961 --hub http://127.0.0.1:18961 --shots <artifact-directory>
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { chromium } from "playwright-core";

const args = process.argv.slice(2);
const flag = (name) => {
  const i = args.indexOf(name);
  return i >= 0 ? args[i + 1] : undefined;
};
const base = (args[0] ?? "").replace(/\/$/, "");
const hub = (flag("--hub") ?? base).replace(/\/$/, "");
const shots = flag("--shots");
const executable = flag("--executable");
if (!base || !shots) {
  console.error("usage: node web/e2e/file-operations.mjs <web URL> [--hub <mock URL>] --shots <artifact-directory> [--executable <chromium>]");
  process.exit(2);
}
mkdirSync(shots, { recursive: true });
setTimeout(() => {
  console.log("FAIL timed out after 3 minutes");
  process.exit(1);
}, 180_000).unref();

const results = [];
const fail = (message) => {
  console.log(`FAIL ${message}`);
  process.exitCode = 1;
  throw new Error(message);
};
const mock = async (path) => {
  const response = await fetch(`${hub}${path}`, { headers: { Authorization: "Bearer mock" }, signal: AbortSignal.timeout(5000) });
  if (!response.ok) fail(`${path}: HTTP ${response.status}`);
  return response.json();
};
const waitFor = async (predicate, what) => {
  const deadline = Date.now() + 12_000;
  let last;
  while (Date.now() < deadline) {
    last = await predicate();
    if (last) return last;
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  fail(`${what}; last=${JSON.stringify(last)}`);
};
const names = async () => new Set((await mock("/api/v1/machines/macbook/fs?path=~")).entries.map((entry) => entry.name));

async function open(browser, colorScheme) {
  const ctx = await browser.newContext({ viewport: { width: 1280, height: 900 }, deviceScaleFactor: 2, colorScheme, locale: "zh-CN" });
  const page = await ctx.newPage();
  const errors = [];
  page.on("pageerror", (error) => errors.push(String(error)));
  await page.goto(`${base}/?token=mock`);
  await page.waitForFunction(() => localStorage.getItem("persona.token"), null, { timeout: 15_000 });
  await page.evaluate((theme) => {
    localStorage.setItem("persona.theme", theme);
    localStorage.setItem("persona.language", "zh-Hans");
  }, colorScheme);
  await page.goto(`${base}/machines/macbook`);
  const files = page.getByRole("button", { name: "文件", exact: true });
  await files.waitFor({ state: "visible", timeout: 20_000 });
  await files.click();
  const more = page.getByRole("button", { name: "「notes.txt」的更多操作", exact: true });
  await more.waitFor({ state: "visible", timeout: 20_000 });
  await more.click();
  return { ctx, page, errors };
}

async function run(browser, colorScheme, kind) {
  const copying = kind === "copy";
  const { ctx, page, errors } = await open(browser, colorScheme);
  try {
    await page.getByText(copying ? "创建副本" : "压缩", { exact: true }).click();
    const progress = page.locator('[role="progressbar"][aria-label="notes.txt"]');
    await progress.waitFor({ state: "visible", timeout: 12_000 });
    await waitFor(async () => Number(await progress.getAttribute("aria-valuenow")) > 0, `${colorScheme} ${kind}: no polled byte progress`);
    const cancelName = copying ? "取消复制「notes.txt」" : "取消压缩「notes.zip」";
    const cancel = page.getByRole("button", { name: cancelName, exact: true });
    await cancel.waitFor({ state: "visible" });
    await page.screenshot({ path: join(shots, `web-${kind}-progress-${colorScheme}.png`) });
    await cancel.click();
    await progress.waitFor({ state: "detached", timeout: 5000 });
    const operation = await waitFor(async () => {
      const operations = (await mock("/mock/file-operations")).operations;
      return operations.find((op) => op.kind === kind && op.state === "cancelled");
    }, `${colorScheme} ${kind}: the mock did not see Cancel`);
    const left = await names();
    const output = copying ? "notes 2.txt" : "notes.zip";
    if (left.has(output)) fail(`${colorScheme} ${kind}: ${output} remained after Cancel`);
    if (errors.length) fail(`${colorScheme} ${kind}: browser errors ${errors.join("; ")}`);
    results.push({ colorScheme, kind, operation: { id: operation.id, state: operation.state }, outputRemoved: true });
    console.log(`ok  ${colorScheme} ${kind}: progress, Cancel and no partial output`);
  } finally {
    await ctx.close();
  }
}

const browser = await chromium.launch(executable ? { executablePath: executable, headless: true } : { channel: "chrome", headless: true });
try {
  await run(browser, "light", "copy");
  await run(browser, "dark", "zip");
  writeFileSync(join(shots, "results.json"), JSON.stringify(results, null, 2) + "\n");
  console.log("PASS web remote file operations");
} finally {
  await browser.close();
}
