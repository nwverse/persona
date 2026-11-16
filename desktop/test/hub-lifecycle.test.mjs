import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import { cpSync } from "node:fs";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
import test from "node:test";

const root = join(import.meta.dirname, "..", "..");
const built = spawnSync(process.platform === "win32" ? "npm.cmd" : "npm", ["--prefix", join(root, "desktop"), "run", "build"], { encoding: "utf8", timeout: 120000, shell: process.platform === "win32" });
assert.equal(built.status, 0, `${built.stdout}\n${built.stderr}`);
cpSync(join(root, "desktop", "dist"), join(root, "desktop", "dist-test", "src"), { recursive: true });
const main = join(root, "desktop", "dist-test", "src", "main.js");
const loader = join(root, "desktop", "test", "fixtures", "lifecycle-register.mjs");

async function run(name) {
  const script = `
    const electron = await import("./fixtures/electron-lifecycle.mjs");
    ${name === "tray-failure" ? "" : "await electron.importMain();"}
    ${name === "stale" ? `
      await electron.app.emit("second-instance", {}, ["persona://link"]);
      await electron.settle();
      const setup = electron.windows.find(window => window.options.title === "Persona setup");
      if (!setup) throw new Error("setup was not created");
      const response = await electron.connect({sender: setup.webContents, senderFrame: setup.webContents}, "https://new.example/");
      if (!response.ok) throw new Error("hub replacement failed");
      electron.controls.get("reject-old")(new Error("ERR_ABORTED: intentionally destroyed stale load"));
    ` : name === "connect-failure" ? `
      electron.scenario.failNew = true;
      await electron.app.emit("second-instance", {}, ["persona://link"]);
      await electron.settle();
      const setup = electron.windows.find(window => window.options.title === "Persona setup");
      if (!setup) throw new Error("setup was not created");
      const response = await electron.connect({sender: setup.webContents, senderFrame: setup.webContents}, "https://new.example/");
      if (response.ok || response.error === "Enter a valid HTTP or HTTPS hub address.") throw new Error("the real connection error was not preserved");
      if (setup.destroyed) throw new Error("setup closed before a failed connection recovered");
      setup.close();
      for (let i = 0; i < 20 && !electron.windows.some(window => window.options.title === "Persona setup" && !window.destroyed); i++) await electron.settle();
      if (electron.windows.some(window => window.options.title === "Persona" && !window.destroyed && !window.url)) throw new Error("a blank hubless main window was created");
      if (!electron.windows.some(window => window.options.title === "Persona setup" && !window.destroyed)) throw new Error("setup recovery did not occur: " + JSON.stringify(electron.events));
    ` : name === "prefix-root" ? `
      electron.scenario.oldSucceeds = true;
      await electron.importMain();
      await electron.app.emit("second-instance", {}, ["persona://link"]);
      await electron.settle();
      const setup = electron.windows.find(window => window.options.title === "Persona setup");
      if (!setup) throw new Error("setup was not created");
      const response = await electron.connect({sender: setup.webContents, senderFrame: setup.webContents}, "https://old.example/");
      if (!response.ok) throw new Error("prefix-to-root replacement failed");
    ` : name === "destroyed-load" ? `
      electron.windows[0].destroy();
      electron.controls.get("reject-old")(new Error("ERR_ABORTED: destroyed during a current-generation load"));
    ` : name === "geometry-failure" ? `
      (await import("./fixtures/lifecycle-store.mjs")).scenario.failWindowSave = true;
      electron.windows[0].emit("resize");
    ` : name === "tray-failure" ? `
      electron.scenario.oldSucceeds = true;
      await electron.importMain();
      for (let i = 0; i < 20 && !electron.events.includes("tray-click-registered"); i++) await electron.settle();
      electron.windows[0].destroy();
      electron.scenario.failNext = true;
      const tray = electron.Tray.instances.at(-1);
      if (!tray) throw new Error("tray was not created: " + JSON.stringify(electron.events));
      tray.emit("click");
      for (let i = 0; i < 20 && !electron.windows.some(window => window.options.title === "Persona setup" && !window.destroyed); i++) await electron.settle();
    ` : `electron.controls.get("reject-old")(new Error("synthetic current-load failure"));`}
    await electron.settle();
    process.stdout.write(JSON.stringify({events: electron.events, unhandled: electron.scenario.unhandled, updates: (await import("./fixtures/lifecycle-store.mjs")).updates, windows: electron.windows.map(window => ({title: window.options.title, url: window.url, destroyed: window.destroyed, partition: window.options.webPreferences.partition}))}));
    process.exit(0);
  `;
  const child = spawn(process.execPath, ["--import", pathToFileURL(loader).href, "--input-type=module", "--eval", script], { cwd: join(root, "desktop", "test"), env: { ...process.env, PERSONA_TEST_MAIN: pathToFileURL(main).href, PERSONA_DESKTOP_TEST_NO_PROTOCOL_REGISTRATION: "1" }, stdio: ["ignore", "pipe", "pipe"] });
  let stdout = "", stderr = "";
  child.stdout.on("data", chunk => { stdout += chunk; });
  child.stderr.on("data", chunk => { stderr += chunk; });
  const code = await new Promise(done => child.once("close", done));
  assert.equal(code, 0, stderr);
  return JSON.parse(stdout);
}

test("a deliberately destroyed old hub load cannot quit the app after its replacement succeeds", async () => {
  const { events, windows } = await run("stale");
  assert.equal(events.includes("app.quit"), false, JSON.stringify(events));
  assert.equal(events.some(value => value.title === "Persona could not start"), false);
  assert.equal(windows.at(-1).url, "https://new.example/");
  assert.equal(windows.at(-1).destroyed, false);
  assert.notEqual(windows.at(-1).partition, windows[0].partition);
});

test("a genuine current-generation load failure still rejects and destroys its window", async () => {
  const { events, windows } = await run("current");
  assert.equal(events.some(value => value.title === "Persona could not start"), true);
  assert.equal(events.includes("app.quit"), true);
  assert.equal(windows[0].destroyed, true);
});

test("destroying a current-generation pending load cannot hide its failure", async () => {
  const { events } = await run("destroyed-load");
  assert.equal(events.some(value => value.title === "Persona could not start"), true, JSON.stringify(events));
  assert.equal(events.includes("app.quit"), true);
});

test("a geometry-save rejection stays local and does not become an unhandled rejection", async () => {
  const { unhandled } = await run("geometry-failure");
  assert.deepEqual(unhandled, []);
});

test("same-origin prefix-to-root replaces the window, partition and saved hub", async () => {
  const { windows, updates } = await run("prefix-root");
  assert.equal(windows.at(-1).url, "https://old.example/");
  assert.notEqual(windows.at(-1).partition, windows[0].partition);
  assert.equal(windows[0].destroyed, true);
  assert.deepEqual(updates, [{ hub: "https://old.example/" }]);
});

test("a failed first-time connection preserves setup and reports the actual error", async () => {
  const { windows, events } = await run("connect-failure");
  const setup = windows.find(window => window.title === "Persona setup" && !window.destroyed);
  assert.ok(setup, JSON.stringify(events));
  assert.equal(windows.some(window => window.title === "Persona" && !window.destroyed && !window.url), false, JSON.stringify(events));
});

test("a failed tray load is handled and returns to setup", async () => {
  const { unhandled, windows, events } = await run("tray-failure");
  assert.deepEqual(unhandled, []);
  assert.equal(windows.some(window => window.title === "Persona setup" && !window.destroyed), true, JSON.stringify({events, windows}));
});
