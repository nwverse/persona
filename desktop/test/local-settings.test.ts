import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { join } from "node:path";
import test from "node:test";
import { parseHubAddress } from "../src/urls.js";
import { HubWindowLifecycle, sameHubWindow } from "../src/hub-window-lifecycle.js";

const source = (name: string) => readFile(join(import.meta.dirname, "..", "..", "src", name), "utf8");

test("an explicit hub change closes the old window before making its own session and IPC", async () => {
  const main = await source("main.ts");
  const change = main.slice(main.indexOf("async function replaceMainWindow"), main.indexOf("async function showMain"));
  assert.match(change, /sameHubWindow\(next, previous\.webContents\.getURL\(\)\)/);
  const hub = parseHubAddress("https://example.test/prefix/");
  assert.equal(sameHubWindow(hub, hub.url.href), true);
  assert.equal(sameHubWindow(hub, `${hub.url.href}chat`), true);
  assert.equal(sameHubWindow(hub, `${hub.url.href}chat/`), true);
  assert.equal(sameHubWindow(hub, "https://example.test/prefix/chat"), true);
  assert.equal(sameHubWindow(parseHubAddress("https://other.example/"), hub.url.href), false);
  assert.equal(sameHubWindow(hub, "https://example.test/"), false);
  assert.match(change, /const closed = new Promise<void>\(done => previous\.once\("closed", done\)\);/);
  assert.ok(change.indexOf("await closed;") < change.indexOf("mainWindow = undefined;"));
  const connect = main.slice(main.indexOf("connect: async"), main.indexOf("await setup.loadURL"));
  assert.ok(connect.indexOf("const generation = hubWindow.generation") < connect.indexOf("await showMain()"));
  assert.ok(connect.indexOf("await showMain()") < connect.indexOf("await store().update({ hub: next.url.href })"));
  assert.ok(connect.indexOf("await store().update({ hub: next.url.href })") < connect.indexOf("setup.close()"));
  assert.match(connect, /generation !== hubWindow\.generation \|\| hub\?\.url\.href !== next\.url\.href/);
  assert.match(connect, /Could not save the hub connection/);
  assert.match(connect, /failedWindow\.destroy\(\)/);
  assert.match(main, /hubWindow\.replace\(\);/);
  assert.match(main, /hub: \(\) => \{\s+const url = current\.webContents\.getURL\(\);/);
  const ipc = await source("ipc.ts");
  assert.match(ipc, /inHub\(hub, event\.senderFrame\.url\)/);
});

test("setup close and Continue share one hub-window creation across the asynchronous state read", async () => {
  const lifecycle = new HubWindowLifecycle();
  let created = 0;
  let release: (() => void) | undefined;
  const reading = new Promise<void>(done => { release = done; });
  const show = () => lifecycle.show(async () => {
    created++;
    await reading;
    return { id: created };
  });
  const fromClosed = show();
  const fromContinue = show();
  assert.equal(created, 1);
  release!();
  const [first, second] = await Promise.all([fromClosed, fromContinue]);
  assert.equal(first, second);
  assert.equal(created, 1);
});

test("replacement starts a new generation while the previous creation is pending", async () => {
  const lifecycle = new HubWindowLifecycle<string>();
  let release: ((error: Error) => void) | undefined;
  const pending = new Promise<string>((_resolve, reject) => { release = reject; });
  const old = lifecycle.show(() => pending);
  const generation = lifecycle.generation;
  lifecycle.replace();
  const replacement = lifecycle.show(async () => "new hub");
  assert.equal(await replacement, "new hub");
  release!(new Error("the old window was destroyed"));
  await assert.rejects(old, /destroyed/);
  assert.ok(lifecycle.generation > generation);
});

test("a failed hub-window creation clears its pending state and permits a retry", async () => {
  const lifecycle = new HubWindowLifecycle<void>();
  let calls = 0;
  await assert.rejects(lifecycle.show(async () => { calls++; throw new Error("synthetic load failure"); }));
  await lifecycle.show(async () => { calls++; });
  assert.equal(calls, 2);
});

test("local login-item IPC is separate from the surviving hub window's public bridge", async () => {
  const setup = await source("setup-ipc.ts");
  const preload = await source("setup-preload.cts");
  for (const suffix of ["get", "set"]) {
    assert.match(setup, new RegExp(`"desktop:setup-login-${suffix}"`));
    assert.match(preload, new RegExp(`"desktop:setup-login-${suffix}"`));
    assert.ok(!setup.includes(`"desktop:login-${suffix}"`), `setup must not own desktop:login-${suffix}`);
  }
  const hub = await source("ipc.ts");
  for (const name of ["desktop:login-get", "desktop:login-set"]) assert.match(hub, new RegExp(`"${name}"`));
});
