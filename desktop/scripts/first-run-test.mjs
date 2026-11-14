// A packaged first-install acceptance test, not a development-window smoke.
//
//   node desktop/scripts/first-run-test.mjs [output directory]
//
// Builds the ordinary unsigned production app for the current host, launches its unchanged entry point with an empty
// profile and no hub environment, and drives its existing windows through a temporary CDP socket on loopback. This is
// not a physical-device test. macOS unsigned CI checks setup and explicitly blocks hub acceptance pending a valid
// signature; PERSONA_DESKTOP_TEST_SIGNED_MAC=1 requires the release job's Developer ID signed, notarized ZIP.
// Windows runs an unpacked build from the same configuration as NSIS so it cannot replace a production-identity
// install. Linux is supplementary, not a shipping target.
import { spawn } from "node:child_process";
import { createHash, randomBytes, randomUUID } from "node:crypto";
import { createReadStream, existsSync } from "node:fs";
import { chmod, mkdir, mkdtemp, readFile, readdir, rm, writeFile } from "node:fs/promises";
import { createServer } from "node:net";
import { homedir, tmpdir } from "node:os";
import { basename, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { createRequire } from "node:module";
import { getCurrentFuseWire, FuseState, FuseV1Options } from "@electron/fuses";
import { DesktopCDP } from "./first-run-cdp.mjs";
import { startGateway } from "./smoke-gateway.mjs";
import { firstRunVerdict } from "./first-run-verdict.mjs";

const require = createRequire(import.meta.url);
const asar = require("@electron/asar");
const root = fileURLToPath(new URL("../..", import.meta.url));
const desktop = join(root, "desktop");
const requestedOutput = resolve(process.argv[2] ?? join(desktop, "release", "first-run"));
const output = join(requestedOutput, randomUUID());
await mkdir(output, { recursive: true });
const platform = process.platform;
const npm = platform === "win32" ? "npm.cmd" : "npm";
const owned = new Set();
const steps = [];
let work, hub, gateway, log = "", failure, macSignature;
let artifactSHA256, buildConfiguration;
const blocked = [];
const clients = [];
const sleep = ms => new Promise(done => setTimeout(done, ms));
const step = (name, ok, detail) => {
  steps.push({ name, ok, ...(detail === undefined ? {} : { detail }) });
  console.log(`${ok ? "ok  " : "FAIL"} ${name}${detail === undefined ? "" : ` ${JSON.stringify(detail)}`}`);
};
function need(name, ok, detail) { step(name, ok, detail); if (!ok) throw new Error(name); }
async function until(label, probe, timeout = 30000) {
  const deadline = Date.now() + timeout;
  let last;
  for (;;) {
    try { last = await probe(); if (last) return last; } catch (error) { if (error?.fatal) throw error; last = String(error); }
    if (Date.now() > deadline) throw new Error(`${label} timed out: ${JSON.stringify(last)}`);
    await sleep(100);
  }
}
function run(command, args, { seconds = 300, env = process.env, quiet = false } = {}) {
  return new Promise((done, fail) => {
    const child = spawn(command, args, { cwd: root, env, shell: platform === "win32" && command.endsWith(".cmd"), stdio: ["ignore", "pipe", "pipe"] });
    owned.add(child);
    let text = "";
    const keep = data => { text += data; if (!quiet) process.stdout.write(data); };
    child.stdout.on("data", keep);
    child.stderr.on("data", keep);
    const timer = setTimeout(() => { stop(child); fail(new Error(`${basename(command)} timed out after ${seconds}s`)); }, seconds * 1000);
    child.once("error", error => { clearTimeout(timer); owned.delete(child); fail(error); });
    child.once("close", code => { clearTimeout(timer); owned.delete(child); done({ code, text }); });
  });
}
function childExited(child) {
  return child.exitCode !== null || child.signalCode !== null;
}
function exited(child, message) {
  const error = new Error(`${message}: ${JSON.stringify({ code: child.exitCode, signal: child.signalCode })}`);
  error.fatal = true;
  return error;
}
async function stop(child) {
  if (!child || childExited(child)) return;
  const closed = new Promise(resolve => child.once("close", resolve));
  try {
    if (platform === "win32") spawn("taskkill", ["/T", "/F", "/PID", String(child.pid)], { stdio: "ignore" });
    else process.kill(-child.pid, "SIGKILL");
  } catch { /* already exited */ }
  // Do not start the next scenario until this app's single-instance identity has gone away.
  let timer;
  try {
    await Promise.race([closed, new Promise((_, reject) => { timer = setTimeout(() => reject(new Error("packaged app did not exit within 10 seconds")), 10000); })]);
  } finally { clearTimeout(timer); }
}
async function freePort() {
  const probe = createServer();
  await new Promise(done => probe.listen(0, "127.0.0.1", done));
  const { port } = probe.address();
  await new Promise(done => probe.close(done));
  return port;
}
async function startHub() {
  const port = await freePort();
  const token = `jp_${randomBytes(18).toString("hex")}`;
  const child = spawn(platform === "win32" ? "python" : "python3", ["-I", join("scripts", "mock", "mock_hub.py")], {
    cwd: root,
    env: { ...process.env, PORT: String(port), MOCK_PAIR_TOKEN: token, MOCK_PAIR_GRACE: "2", MOCK_DESKTOPS: "1", PYTHONIOENCODING: "utf-8", PYTHONUTF8: "1" },
    stdio: ["ignore", "pipe", "pipe"], detached: platform !== "win32",
  });
  owned.add(child);
  let text = "";
  child.stdout.on("data", data => { text = (text + data).slice(-16000); });
  child.stderr.on("data", data => { text = (text + data).slice(-16000); });
  child.once("error", error => { text += `\n${error}\n`; });
  await until("synthetic hub health", async () => {
    if (childExited(child)) throw exited(child, `synthetic hub exited before ready:\n${text}`);
    return (await fetch(`http://127.0.0.1:${port}/api/v1/health`, { signal: AbortSignal.timeout(1000) })).ok;
  }, 90000);
  return { port, token, text: () => text };
}
async function build() {
  need("compile the production app", (await run(npm, ["--prefix", "desktop", "run", "build"], { quiet: true })).code === 0);
  if (process.env.PERSONA_DESKTOP_TEST_PACKAGE) {
    need("the preceding package step produced the explicit test artifact", existsSync(resolve(process.env.PERSONA_DESKTOP_TEST_PACKAGE)), process.env.PERSONA_DESKTOP_TEST_PACKAGE);
    return;
  }
  if (!existsSync(join(root, "web", "dist", "index.html"))) {
    need("install the web client for the local hub", (await run(npm, ["--prefix", "web", "ci"], { seconds: 900, quiet: true })).code === 0);
    need("build the web client for the local hub", (await run(npm, ["--prefix", "web", "run", "build"], { seconds: 1200, quiet: true })).code === 0);
  }
  const target = platform === "win32" ? ["--win", "dir", "--x64"] : platform === "darwin" ? ["--mac", "--universal"] : ["--linux", "dir", "--x64"];
  const result = await run(process.execPath, [join(desktop, "node_modules", "electron-builder", "cli.js"), "--projectDir", desktop, "--config", buildConfiguration, ...target, "--publish", "never"], {
    cwd: root, seconds: 1500,
    env: { ...process.env, PERSONA_DESKTOP_APP_ID: "com.example.persona.desktop", CSC_IDENTITY_AUTO_DISCOVERY: "false" },
  });
  need("build the ordinary unsigned production package", result.code === 0, result.code);
}
async function executable() {
  const supplied = process.env.PERSONA_DESKTOP_TEST_PACKAGE;
  if (supplied && platform === "linux") throw new Error("The explicit package input is supported for macOS and Windows");
  if (platform === "win32") {
    // Run the exact app files packaged by this build, not the production-identity NSIS installer. Installing that
    // would replace an existing per-user installation on a persistent runner. The isolated-AppId update test owns
    // NSIS install/update acceptance; this gate covers first-run loading and the setup/Link/hub path.
    const file = supplied ? resolve(supplied) : join(desktop, "release", "win-unpacked", "Persona.exe");
    need("the packaged Windows executable exists", existsSync(file), file);
    return { file, installed: undefined };
  }
  if (platform === "darwin") {
    const candidates = supplied ? [resolve(supplied)] : (await readdir(join(desktop, "release"))).filter(name => /^Persona-.*-mac-universal\.zip$/.test(name)).map(name => join(desktop, "release", name));
    if (candidates.length !== 1) throw new Error(`expected one macOS universal ZIP, got ${candidates.length}`);
    const archive = candidates[0];
    const installed = join(work, "installed");
    await mkdir(installed);
    need("unpack the macOS ZIP", (await run("ditto", ["-x", "-k", archive, installed])).code === 0);
    const file = join(installed, "Persona.app", "Contents", "MacOS", "Persona");
    const identity = await run("codesign", ["-dvvv", join(installed, "Persona.app")], { quiet: true });
    const authority = identity.text.match(/^Authority=(.+)$/m)?.[1];
    const team = identity.text.match(/^TeamIdentifier=(.+)$/m)?.[1];
    if (process.env.PERSONA_DESKTOP_TEST_SIGNED_MAC === "1") {
      // The default Developer ID designated requirement is system-synthesized and need not contain a literal
      // team-identifier token. Use the verified signature and Gatekeeper's trust decision, not a DR string prefix.
      need("the release package has a consistent Developer ID team signature", identity.code === 0 && authority?.startsWith("Developer ID Application:") && !!team && team !== "not set", { authority: authority ?? "unavailable", team: team ?? "unavailable" });
      need("macOS verifies the signed package's signature and notarization", (await run("codesign", ["--verify", "--deep", "--strict", "--verbose=2", join(installed, "Persona.app")], { quiet: true })).code === 0 && (await run("spctl", ["--assess", "--type", "execute", "--verbose=2", join(installed, "Persona.app")], { quiet: true })).code === 0, "signature classification is Developer ID; codesign/spctl output is withheld from evidence");
      macSignature = "developer-id";
    } else {
      // This launches the unsigned development artifact; it is not Developer ID acceptance and must not change its
      // bundle identifier or designated requirement to make Keychain behave differently.
      need("ad-hoc signature only for the unsigned development launch", (await run("codesign", ["--force", "--deep", "--sign", "-", join(installed, "Persona.app")])).code === 0);
      macSignature = "ad-hoc";
    }
    return { file, installed };
  }
  return { file: join(desktop, "release", "linux-unpacked", "persona-desktop"), installed: undefined };
}
async function fuseCheck(file) {
  const fuses = await getCurrentFuseWire(file);
  const expected = {
    RunAsNode: false, EnableCookieEncryption: true, EnableNodeOptionsEnvironmentVariable: false,
    EnableNodeCliInspectArguments: false, EnableEmbeddedAsarIntegrityValidation: true,
    OnlyLoadAppFromAsar: true, GrantFileProtocolExtraPrivileges: false,
  };
  const actual = Object.fromEntries(Object.entries(expected).map(([key, value]) => [key, fuses[FuseV1Options[key]] === (value ? FuseState.ENABLE : FuseState.DISABLE)]));
  need("the packaged fuses retain the security boundary", Object.values(actual).every(Boolean), actual);
}
async function packageCheck(file) {
  const archive = platform === "darwin" ? join(file, "..", "..", "Resources", "app.asar") : platform === "win32" ? join(file, "..", "resources", "app.asar") : join(desktop, "release", "linux-unpacked", "resources", "app.asar");
  const files = asar.listPackage(archive).map(name => `/${name.replaceAll("\\", "/").replace(/^\/+/, "")}`);
  const publicResources = ["/dist/setup/setup.html", "/dist/setup/setup.css", "/dist/setup/setup.cjs"];
  const allowed = new Set(publicResources);
  const missing = publicResources.filter(name => !files.includes(name));
  const unexpected = files.filter(name => name.startsWith("/dist/setup/") && !allowed.has(name));
  step("packaged setup resource listing", true, { archive, setup: files.filter(name => name.startsWith("/dist/setup")), sample: files.slice(0, 40) });
  need("only the three public resources enter the setup directory", missing.length === 0 && unexpected.length === 0, { missing, unexpected, archive });
  need("no test driver or privileged preload enters the public resource directory", !files.some(name => name.includes("first-run")) && !files.includes("/dist/setup/setup-preload.cjs") && files.includes("/dist/setup-preload.cjs"));
}
async function launch(file, profile, home, linkLog) {
  const debugPort = await freePort();
  const env = { ...process.env, HOME: home, USERPROFILE: home, PERSONA_DESKTOP_DISABLE_UPDATE: "1", PERSONA_DESKTOP_TEST_NO_PROTOCOL_REGISTRATION: "1", ...(linkLog ? { PERSONA_TEST_LINK_LOG: linkLog } : {}) };
  for (const name of Object.keys(env)) if (name === "PERSONA_HUB_URL" || name.startsWith("PERSONA_DESKTOP_E2E") || name === "PERSONA_DESKTOP_SMOKE" || name === "ELECTRON_RUN_AS_NODE" || name === "NODE_OPTIONS") delete env[name];
  for (const name of ["PERSONA_DESKTOP_REQUIRE_SIGNING", "PERSONA_DESKTOP_TEST_SIGNED_MAC", "PERSONA_DESKTOP_TEST_PACKAGE", "PERSONA_DESKTOP_TEST_BUILD_CONFIG", "PERSONA_DESKTOP_APP_ID", "CSC_LINK", "CSC_KEY_PASSWORD", "CSC_IDENTITY_AUTO_DISCOVERY", "APPLE_API_KEY", "APPLE_API_KEY_P8", "APPLE_API_KEY_ID", "APPLE_API_ISSUER"]) delete env[name];
  need("build and signing environment stays outside the app", !env.PERSONA_DESKTOP_REQUIRE_SIGNING && !env.PERSONA_DESKTOP_TEST_SIGNED_MAC && !env.CSC_LINK && !env.APPLE_API_KEY && !env.APPLE_API_KEY_P8);
  need("the packaged app has no Node runtime overrides", !env.ELECTRON_RUN_AS_NODE && !env.NODE_OPTIONS);
  const args = [`--user-data-dir=${profile}`, `--remote-debugging-address=127.0.0.1`, `--remote-debugging-port=${debugPort}`];
  if (platform === "linux") args.unshift("--disable-gpu");
  delete env.CHROME_DEVEL_SANDBOX;
  delete env.ELECTRON_DISABLE_SANDBOX;
  const forbiddenSandboxFlag = args.find(arg => /^--(?:no-sandbox|disable-setuid-sandbox|disable-namespace-sandbox|no-zygote-sandbox)(?:=|$)/.test(arg));
  need("the acceptance launcher does not disable the Chromium sandbox", !forbiddenSandboxFlag && !env.CHROME_DEVEL_SANDBOX && !env.ELECTRON_DISABLE_SANDBOX);
  const child = spawn(file, args, { cwd: work, env, stdio: ["ignore", "pipe", "pipe"], detached: platform !== "win32" });
  owned.add(child);
  const keep = data => { log += data; };
  child.stdout.on("data", keep);
  child.stderr.on("data", keep);
  child.once("close", () => owned.delete(child));
  child.once("error", error => { log += `${error}\n`; });
  await until("the app's own temporary CDP socket", async () => {
    if (childExited(child)) throw exited(child, `packaged app exited before first run:\n${log}`);
    return existsSync(join(profile, "DevToolsActivePort"));
  }, 30000);
  await sleep(200); // Let the just-announced target finish attaching before querying it.
  const client = new DesktopCDP(profile, output);
  await client.connect();
  clients.push(client);
  return { child, client };
}
async function screenshot(page, name) {
  await page.capture(`${name}.png`);
  await until(`${name} is painted`, () => page.evaluate("document.fonts.status === 'loaded' && document.getAnimations().every(a => a.playState !== 'running' || a.effect?.getComputedTiming().iterations === Infinity)"), 15000);
  await page.capture(`${name}.png`);
}
async function absentScenario(file) {
  const home = join(work, "home-absent");
  const profile = join(work, "profile-absent");
  await mkdir(home, { recursive: true });
  need("the first profile is empty and no hub is supplied", !existsSync(profile) && !process.env.PERSONA_HUB_URL);
  const { child, client } = await launch(file, profile, home);
  clients.push(client);
  const setup = await client.pageFor("the first-run setup page", target => target.url === "persona-setup://app/setup.html");
  need("the packaged setup document loads from the trusted local scheme", setup.url() === "persona-setup://app/setup.html");
  await until("setup localization and Link status", async () => (await setup.evaluate("document.querySelector('h1')?.textContent.length > 0 && !document.querySelector('#link-state')?.textContent.includes('…')")) === true);
  await setup.evaluate("document.querySelector('details').open = true; document.querySelector('#pair').scrollIntoView({block:'center'}); true");
  await screenshot(setup, "01-setup-no-link");
  await until("the absent Link guidance is laid out", async () => {
    const layout = await setup.evaluate("({open:document.querySelector('details').open, height:document.querySelector('#link-install').getBoundingClientRect().height, disabled:document.querySelector('#link-install').disabled})");
    return layout?.open === true && layout.height > 0 && layout.disabled === true;
  });
  need("the production preload initialized the setup page", await setup.evaluate("typeof window.desktopSetup === 'object' && typeof window.require === 'undefined' && typeof window.process === 'undefined' && document.querySelector('#hub').value === ''"));
  need("Link is optional and absent, not silently installed", await setup.evaluate("(async () => { const status = await window.desktopSetup.linkStatus(); return status.available === false && document.querySelector('#link-reconnect').disabled && document.querySelector('#pair-link').disabled && document.querySelector('#pair button').disabled && document.querySelector('#node-install').hidden && !document.querySelector('#link-install').hidden && document.querySelector('#link-install').disabled; })()"));
  need("an unknown local-resource path cannot read another packaged file", !(await setup.evaluate("fetch('persona-setup://app/setup-preload.cjs').then(r => r.ok).catch(() => false)")) && !(await setup.evaluate("fetch('persona-setup://app/../main.js').then(r => r.ok).catch(() => false)")));
  need("the setup window refuses navigation away from its trusted origin", await setup.evaluate("location.href = 'https://example.com/'; true").then(() => sleep(500)).then(() => setup.url() === "persona-setup://app/setup.html"));
  await setup.evaluate("document.querySelector('#hub').value = 'persona-setup://app/setup.html'; document.querySelector('#connect').requestSubmit(); true");
  await until("invalid hub address stays recoverable", () => setup.evaluate("!document.querySelector('#error').hidden && !!document.querySelector('#error').textContent"));
  need("an invalid hub does not get saved or open a window", setup.url() === "persona-setup://app/setup.html" && !existsSync(join(profile, "desktop-state.json")));
  await stop(child);
}
async function pairedScenario(file) {
  const home = join(work, "home-link");
  const profile = join(work, "profile-link");
  const bin = join(home, ".persona", "bin");
  await mkdir(bin, { recursive: true });
  const record = join(work, "link-commands.jsonl");
  const fake = `const fs = require("node:fs");
const argumentsForLink = process.argv.slice(2);
let input = "";
process.stdin.on("data", chunk => input += chunk);
process.stdin.on("end", () => {
  fs.appendFileSync(${JSON.stringify(record)}, JSON.stringify({ argv: argumentsForLink, stdin: input }) + "\\n");
  if (argumentsForLink[0] === "status") {
    console.log(JSON.stringify({ running: true, machine: "Example computer", link: { state: "connected" }, token: "must-not-reach-the-renderer", config: "/must-not-appear" }));
  } else if (argumentsForLink[0] === "pair" && argumentsForLink[1] === "-") {
    console.log("Paired the example computer");
  } else { process.exitCode = 2; }
});
`;
  // The native Windows fixture logs argv/stdin; no runtime overrides are needed on either side.
  const fakeFile = join(work, "fake-link.cjs");
  await writeFile(fakeFile, fake, { mode: 0o700 });
  if (platform !== "win32") {
    await writeFile(join(bin, "persona-link"), `#!/bin/sh\nexec ${JSON.stringify(process.execPath)} ${JSON.stringify(fakeFile)} "$@"\n`, { mode: 0o700 });
    await chmod(join(bin, "persona-link"), 0o700);
  } else {
    const exe = join(bin, "persona-link.exe");
    const compiler = join(process.env.WINDIR ?? "C:\\Windows", "Microsoft.NET", "Framework64", "v4.0.30319", "csc.exe");
    const fixture = join(desktop, "test", "fixtures", "fake-persona-link.cs");
    const compiled = await run(compiler, ["/nologo", "/target:exe", `/out:${exe}`, fixture], { seconds: 120, quiet: true });
    need("compile the native Windows synthetic Link", compiled.code === 0, compiled.text);
    const probe = await run(exe, ["status", "--json"], { env: { ...process.env, PERSONA_TEST_LINK_LOG: record }, quiet: true });
    need("the synthetic Link answers status before app launch", probe.code === 0 && JSON.parse(probe.text).machine === "Example computer", probe.text);
  }
  const { child, client } = await launch(file, profile, home, platform === "win32" ? record : undefined);
  clients.push(client);
  const setup = await client.pageFor("setup with the separately installed test Link", target => target.url === "persona-setup://app/setup.html");
  await until("redacted Link state", () => setup.evaluate("document.querySelector('#link-state')?.textContent.includes('connected') && !document.querySelector('#pair-link').disabled"));
  await setup.evaluate("document.querySelector('details').open = true; true");
  await until("the paired Link form is laid out", () => setup.evaluate("document.querySelector('#pair-link').getBoundingClientRect().height > 0 && !document.querySelector('#pair-link').disabled"));
  await setup.evaluate("document.querySelector('#pair').scrollIntoView({block:'center'}); true");
  await screenshot(setup, "02-setup-with-link");
  const linkURL = "persona://link?join=synthetic-example&code=synthetic-code";
  await setup.evaluate(`document.querySelector('#pair-link').value = ${JSON.stringify(linkURL)}; document.querySelector('#pair').requestSubmit(); true`);
  await until("the fixed Link pairing command", async () => (await readFile(record, "utf8").catch(() => "")).includes('"pair"'));
  const commands = (await readFile(record, "utf8")).split("\n").filter(Boolean).map(line => JSON.parse(line));
  need("Link pairing uses fixed argv and stdin, with no service installation", commands.some(command => command.argv.join(" ") === "pair -" && command.stdin === linkURL) && commands.every(command => command.argv[0] === "status" || command.argv.join(" ") === "pair -"), commands);
  need("status and command output stay redacted", !(await setup.evaluate("document.body.innerText")).includes("must-not-reach-the-renderer") && !(await setup.evaluate("document.body.innerText")).includes("must-not-appear"));
  if (macSignature === "ad-hoc") {
    blocked.push({
      scenario: "hub setup, web pairing and chat",
      reason: "EnableCookieEncryption requires a valid consistent macOS code signature for Keychain access; an ad-hoc development signature is not signed-release acceptance",
      uncovered: ["submit the hub address", "web sign-in and pairing", "enter the existing chat page"],
      requiredEnvironment: "the administrator-approved Developer ID signed and notarized release workflow",
    });
    await stop(child);
    return;
  }
  await setup.evaluate(`document.querySelector('#hub').value = ${JSON.stringify(gateway.url)}; document.querySelector('#connect').requestSubmit(); true`);
  const main = await client.pageFor("the hub's own login page", target => target.url.startsWith(gateway.url));
  await until("hub login form", () => main.evaluate("!!document.querySelector('form input[type=text]')"));
  need("setup saved the exact hub and opened the hub origin", JSON.parse(await readFile(join(profile, "desktop-state.json"), "utf8")).hub === gateway.url && main.url() === gateway.url);
  await screenshot(main, "03-hub-sign-in");
  need("the hub renderer keeps Node unavailable and receives the existing bridge", await main.evaluate("typeof require === 'undefined' && typeof process === 'undefined' && window.personaDesktop.version === 1"));
  await main.evaluate(`(() => { const input = document.querySelector('form input[type=text]'); const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set; setter.call(input, ${JSON.stringify(`persona://pair?url=${gateway.url}&token=${hub.token}`)}); input.dispatchEvent(new Event('input', { bubbles: true })); input.dispatchEvent(new Event('change', { bubbles: true })); input.closest('form').requestSubmit(); return true; })()`);
  await until("signed-in hub UI through its own pairing flow", () => main.evaluate("!!window.personaDesktop && !location.search.includes('token') && !document.querySelector('input[type=password]') && !!document.querySelector('nav a[href], aside a[href]')"), 60000);
  await until("the synthetic hub received the pairing exchange", () => gateway.requests().some(entry => entry.path === "/api/v1/auth/tokens" && entry.status === 200));
  await main.evaluate("history.pushState({}, '', '/'); window.dispatchEvent(new PopStateEvent('popstate')); true");
  await until("the hub's main chat content", () => main.evaluate("!!document.querySelector('main [data-chat-content]')"));
  await screenshot(main, "04-entered-hub");
  need("the packaged first-run path reaches the existing web app", main.url().startsWith(gateway.url));
  await stop(child);
}

async function main() {
  if (platform === "linux") need("a display is available", !!(process.env.DISPLAY || process.env.WAYLAND_DISPLAY), "run under Xvfb; Linux is supplementary evidence");
  if (process.env.PERSONA_DESKTOP_TEST_PACKAGE && process.env.PERSONA_DESKTOP_TEST_BUILD_CONFIG) {
    throw new Error("An existing artifact is checked directly; a separate build configuration cannot change it");
  }
  const suppliedConfig = process.env.PERSONA_DESKTOP_TEST_BUILD_CONFIG;
  const config = suppliedConfig ? resolve(suppliedConfig) : join(desktop, "electron-builder.unsigned.yml");
  if (process.env.PERSONA_DESKTOP_TEST_PACKAGE) {
    // The preceding package job owns the builder configuration. This driver executes, never rebuilds, that artifact.
    buildConfiguration = "not-rebuilt-supplied-artifact";
  } else if (!suppliedConfig || config === join(desktop, "electron-builder.unsigned.yml")) {
    buildConfiguration = config;
  } else {
    const source = JSON.parse(await readFile(config, "utf8"));
    if (resolve(config, "..", source.extends) !== join(desktop, "electron-builder.unsigned.yml") || Object.keys(source).some(key => key !== "extends" && key !== "electronDownload") || typeof source.electronDownload?.checksums !== "object") {
      throw new Error("A local build config may only extend the checked unsigned config with Electron download checksums");
    }
    buildConfiguration = config;
  }
  if (process.env.PERSONA_DESKTOP_TEST_SIGNED_MAC === "1" && platform !== "darwin") {
    throw new Error("The signed first-run gate currently accepts only a macOS release artifact");
  }
  if (platform === "darwin" && process.env.PERSONA_DESKTOP_TEST_SIGNED_MAC === "1" && !process.env.PERSONA_DESKTOP_TEST_PACKAGE) {
    throw new Error("The signed first-run gate requires the exact universal ZIP produced by the release package step");
  }
  work = await mkdtemp(join(tmpdir(), "persona-first-run-test-"));
  await mkdir(output, { recursive: true });
  await build();
  const { file, installed } = await executable();
  if (platform === "darwin" && !process.env.PERSONA_DESKTOP_TEST_PACKAGE) {
    // The locally produced .app is the tested artifact; CI hashes the exact supplied universal ZIP.
    const hash = createHash("sha256");
    for await (const chunk of createReadStream(file)) hash.update(chunk);
    artifactSHA256 = `universal-app-executable:${hash.digest("hex")}`;
  } else {
    const archive = platform === "darwin" ? resolve(process.env.PERSONA_DESKTOP_TEST_PACKAGE) : file;
    const hash = createHash("sha256");
    for await (const chunk of createReadStream(archive)) hash.update(chunk);
    artifactSHA256 = hash.digest("hex");
  }
  await fuseCheck(file);
  await packageCheck(file);
  hub = await startHub();
  gateway = await startGateway({ dist: join(root, "web", "dist"), mockPort: hub.port });
  await absentScenario(file);
  await pairedScenario(file);
  return { installed: installed ? "temporary test directory" : "unpacked directory", evidence: `${platform}/${process.arch}` };
}

async function cleanup() {
  for (const child of owned) await stop(child);
  await sleep(owned.size ? 2000 : 0);
  await gateway?.close().catch(() => {});
  for (const client of clients) client.close();
  if (work) await rm(work, { recursive: true, force: true, maxRetries: 5, retryDelay: 300 }).catch(() => {});
}
const timeout = setTimeout(() => { failure = "overall test timeout"; for (const child of owned) stop(child); }, 20 * 60 * 1000);
let result;
try { result = await main(); } catch (error) { failure = error instanceof Error ? error.message : String(error); }
await cleanup().catch(error => { failure ??= String(error); });
clearTimeout(timeout);
await mkdir(output, { recursive: true });
const report = JSON.stringify({
  ...firstRunVerdict({ platform, signature: macSignature, failure, blocked, passed: steps.length }),
  arch: process.arch,
  shippingPlatform: platform !== "linux",
  ...(failure ? { failure } : {}),
  ...(artifactSHA256 ? { artifactSHA256 } : {}),
  ...(buildConfiguration ? { buildConfiguration } : {}),
  ...(process.env.PERSONA_DESKTOP_TEST_PACKAGE ? { sourceArtifact: process.env.PERSONA_DESKTOP_TEST_PACKAGE } : {}),
  artifactKind: platform === "darwin" ? "universal-zip" : "unpacked",
  installationAccepted: false,
  signedReleaseAccepted: false,
  ...result,
  steps,
  scenarios: clients.map(client => client.diagnostics()),
}, null, 2);
const clean = value => value.replaceAll(work ?? "<temporary directory>", "<temporary directory>").replaceAll(homedir(), "<home>").replaceAll(hub?.token ?? "<no token>", "<run token>");
await writeFile(join(output, "result.json"), `${clean(report)}\n`, "utf8");
await writeFile(join(output, "electron.log"), clean(log), "utf8");
await writeFile(join(output, "hub.log"), clean(hub?.text() ?? ""), "utf8");
const verdict = firstRunVerdict({ platform, signature: macSignature, failure, blocked, passed: steps.length });
console.log(`Desktop packaged first-run (${platform}/${process.arch}): ${verdict.label}; evidence ${output}`);
process.exitCode = verdict.ok && steps.length > 0 && steps.every(step => step.ok) ? 0 : 1;
