import { spawn } from "node:child_process";
import { randomBytes } from "node:crypto";
import { existsSync } from "node:fs";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import electron from "electron";
import { startGateway } from "./smoke-gateway.mjs";
import { startHub } from "./child-process.mjs";

/**
 * Development-only acceptance smoke: a real Electron process against the repository's synthetic hub, serving the built
 * web client the way the hub does, once at the origin root, once below a reverse-proxy prefix, and once with an
 * explicit Continue to a second hub origin. Every service runs on a port this script owns and is stopped by it; the hub refuses every request without a credential, and the app signs
 * in with a temporary pairing code made for this run.
 *
 *   node desktop/scripts/smoke-with-hub.mjs [output directory]     (default desktop/release/smoke)
 *
 * Writes <output>/<scenario>/{result.json,*.png,electron.log} and <output>/result.json, and ends with one verdict line.
 */
const root = fileURLToPath(new URL("../..", import.meta.url));
const desktop = join(root, "desktop");
const output = resolve(process.argv[2] ?? process.env.PERSONA_DESKTOP_SMOKE ?? join(desktop, "release", "smoke"));
const dist = resolve(process.env.PERSONA_DESKTOP_SMOKE_WEB ?? join(root, "web", "dist"));
const windows = process.platform === "win32";
const npm = windows ? "npm.cmd" : "npm";
const python = windows ? "python" : "python3";
const PREFIX = "/persona";

function run(command, args, options = {}) {
  return new Promise(done => {
    const child = spawn(command, args, { cwd: root, stdio: "inherit", shell: windows && command.endsWith(".cmd"), ...options });
    child.once("error", () => done(127));
    child.once("close", code => done(code ?? 1));
  });
}

async function scenario(name, { prefix, token, starting }) {
  const directory = join(output, name);
  let hub, other, gateway, otherGateway, result, profile;
  try {
    await rm(directory, { recursive: true, force: true }); // a previous run's verdict must not stand in for this one
    await mkdir(directory, { recursive: true });
    profile = await mkdtemp(join(tmpdir(), "persona-desktop-e2e-"));
    const started = await starting;
    if (started.error) throw started.error;
    hub = started.hub;
    gateway = await startGateway({ dist, mockPort: hub.port, prefix });
    if (name === "hub-change" || name === "hub-prefix-parent") {
      // A second hub proves that Continue replaces both the live window and its partition. The prefix-parent case
      // deliberately shares the origin: /old/ is inside /, but it is not the same hub session.
      const token = `jp_${randomBytes(18).toString("hex")}`;
      other = await startHub(root, { MOCK_PAIR_TOKEN: token, MOCK_PAIR_GRACE: "30", MOCK_DESKTOPS: "1" }, 1, { python });
      other.token = token;
      otherGateway = await startGateway({ dist, mockPort: other.port, prefix: "" });
    }
    const direct = `http://127.0.0.1:${hub.port}/api/v1`;
    if ((await fetch(`${direct}/sessions`, { signal: AbortSignal.timeout(3000) })).status !== 401) throw new Error("The synthetic hub answers without a credential");
    // The pairing code opens the hub until the page trades it: pick the waiting card the session screenshot shows.
    const pending = await (await fetch(`${direct}/interactions`, { headers: { Authorization: `Bearer ${token}` }, signal: AbortSignal.timeout(5000) })).json();
    const card = (Array.isArray(pending) ? pending : pending.interactions ?? []).find(ix => ix.session_id && (ix.kind === "permission" || ix.kind === "question"));
    // Without a display server (a Linux build machine) Chromium's headless platform still lays out and paints the page.
    const headless = process.platform === "linux" && !process.env.DISPLAY && !process.env.WAYLAND_DISPLAY;
    const args = ["--no-sandbox", "--disable-gpu", ...(headless ? ["--ozone-platform=headless"] : []), `--user-data-dir=${profile}`, join(desktop, "scripts", "e2e-main.mjs"), "--desktop-e2e"];
    const child = spawn(electron, args, {
      cwd: desktop,
      env: {
        ...process.env, PERSONA_HUB_URL: gateway.url, PERSONA_DESKTOP_DISABLE_UPDATE: "1", PERSONA_DESKTOP_E2E: directory,
        PERSONA_DESKTOP_E2E_SCENARIO: name, PERSONA_DESKTOP_E2E_MOCK: `http://127.0.0.1:${hub.port}`, PERSONA_DESKTOP_E2E_TOKEN: token,
        ...(other && otherGateway ? {
          PERSONA_DESKTOP_E2E_OTHER_MOCK: `http://127.0.0.1:${other.port}`,
          PERSONA_DESKTOP_E2E_OTHER_HUB_URL: otherGateway.url,
          PERSONA_DESKTOP_E2E_OTHER_TOKEN: other.token,
        } : {}),
        PERSONA_DESKTOP_E2E_CARD: JSON.stringify(card ? { id: card.id, kind: card.kind, machine_id: card.machine_id, session_id: card.session_id } : null),
      },
      stdio: ["ignore", "pipe", "pipe"],
    });
    let log = "";
    child.stdout.on("data", data => { log += data; });
    child.stderr.on("data", data => { log += data; });
    const timer = setTimeout(() => child.kill("SIGKILL"), 180_000);
    const exit = await new Promise(done => child.once("close", (code, signal) => done({ code, signal })));
    clearTimeout(timer);
    await writeFile(join(directory, "electron.log"), log.replaceAll(profile, "<temporary profile>").replaceAll(token, "<run token>").replaceAll(other?.token ?? "no-second-pairing-code", "<second run token>"), "utf8");
    try { result = JSON.parse(await readFile(join(directory, "result.json"), "utf8")); } catch { result = { ok: false, error: "No result was written", steps: [] }; }
    if (exit.code !== 0) result = { ...result, ok: false, error: result.error ?? `Electron exited with ${JSON.stringify(exit)}`, exit };
    else result = { ...result, exit };
    const outside = gateway.outside();
    if (prefix && outside.length) result = { ...result, ok: false, error: result.error ?? `Requests left the prefix: ${outside.slice(0, 5).join(", ")}` };
    result = { ...result, outsidePrefix: outside, otherLocalRequests: gateway.strangers(), headless, hubStartMs: hub.startMs, hubStartAttempts: hub.attempts };
    if (!result.ok) console.error(`${name}: ${result.error}\n${log.slice(-3000)}\n${hub.log().slice(-1500)}`);
  } catch (error) {
    result = { ok: false, error: error instanceof Error ? error.message : String(error), steps: [] };
    console.error(`${name}: ${result.error}`);
  } finally {
    const cleanupFailures = [];
    await otherGateway?.close().catch(error => { cleanupFailures.push(error); });
    await other?.stop().catch(error => { cleanupFailures.push(error); });
    await gateway?.close().catch(error => { cleanupFailures.push(error); });
    await (await starting).hub?.stop().catch(error => { cleanupFailures.push(error); });
    if (profile) await rm(profile, { recursive: true, force: true, maxRetries: 5, retryDelay: 300 }).catch(error => { cleanupFailures.push(error); });
    if (cleanupFailures.length) result = { ...result, ok: false, error: result.error ?? `Cleanup failed: ${cleanupFailures.map(error => error instanceof Error ? error.message : String(error)).join("; ")}` };
  }
  await mkdir(directory, { recursive: true });
  await writeFile(join(directory, "result.json"), `${JSON.stringify(result, null, 2)}\n`, "utf8");
  return result;
}

let failure;
let plans = [];
const scenarios = {};
try {
  if (await run(npm, ["--prefix", "desktop", "run", "build"]) !== 0) throw new Error("The desktop build failed");
  if (!existsSync(join(dist, "index.html"))) {
    console.log(`No built web client at ${dist}: building it (needs Go for the sign-in module)`);
    if (await run(npm, ["--prefix", "web", "run", "build"]) !== 0) throw new Error("The web build failed");
  }
  await mkdir(output, { recursive: true });
  // The root hub lists desktop_unregister; the prefixed one is an older hub, where only closing the socket withdraws.
  // The third scenario owns another hub origin for its explicit signed-in Continue action.
  // Both hubs start now, each with a temporary pairing code as `personad hub pair` makes (the page trades it for its
  // own device credential): on a hosted macOS runner a hub started after the first Electron run took over 30 s.
  plans = [["root", "", { MOCK_DESKTOPS: "1" }], ["subpath", PREFIX, {}], ["hub-change", "", { MOCK_DESKTOPS: "1" }], ["hub-prefix-parent", PREFIX, { MOCK_DESKTOPS: "1" }]].map(([name, prefix, hubEnv]) => {
    const token = `jp_${randomBytes(18).toString("hex")}`;
    const starting = startHub(root, { MOCK_PAIR_TOKEN: token, MOCK_PAIR_GRACE: "2", ...hubEnv }, 1, { python }).then(hub => ({ hub }), error => ({ error }));
    return { name, prefix, token, starting };
  });
  for (const plan of plans) scenarios[plan.name] = await scenario(plan.name, plan);
} catch (error) {
  failure = error instanceof Error ? error.message : String(error);
  console.error(failure);
}
const names = Object.keys(scenarios);
const ok = !failure && names.length === plans.length && names.every(name => scenarios[name].ok === true);
const line = `Desktop acceptance smoke (${process.platform}): ${ok ? "PASS" : "FAIL"}${names.length ? ` (${names.map(name => `${name} ${scenarios[name].ok ? "PASS" : "FAIL"}, ${scenarios[name].steps.filter(s => s.ok).length}/${scenarios[name].steps.length} steps`).join("; ")})` : ""}${failure ? `: ${failure}` : ""}`;
await mkdir(output, { recursive: true });
await writeFile(join(output, "result.json"), `${JSON.stringify({
  ok, verdict: line, platform: process.platform, web: "built web client behind a local gateway", ...(failure ? { error: failure } : {}),
  scenarios: Object.fromEntries(names.map(name => [name, { ok: scenarios[name].ok, ...(scenarios[name].error ? { error: scenarios[name].error } : {}), steps: scenarios[name].steps.map(s => ({ name: s.name, ok: s.ok })), screenshots: (scenarios[name].screenshots ?? []).map(s => `${name}/${s.name}`) }])),
}, null, 2)}\n`, "utf8");
console.log(line);
process.exitCode = ok ? 0 : 1;
