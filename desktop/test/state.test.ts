import assert from "node:assert/strict";
import test from "node:test";
import { mkdtemp, readFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { JSONStore } from "../src/store.js";
import { restoreWindow } from "../src/window-state.js";
import { notificationRoute } from "../src/contract.cjs";

const display = { x: 0, y: 0, width: 1920, height: 1080 };

test("window restore clamps sizes and rejects offscreen coordinates", () => {
  assert.deepEqual(restoreWindow({ x: 100, y: 100, width: 1200, height: 800, maximized: true }, [display]), { x: 100, y: 100, width: 1200, height: 800, maximized: true });
  assert.deepEqual(restoreWindow({ x: 5000, y: 100, width: 1200, height: 800 }, [display]), { width: 1200, height: 800, maximized: false });
  assert.equal(restoreWindow({ width: -1, height: Infinity }, [display]).width, 1280);
  assert.equal(restoreWindow(null, [display]).maximized, false);
});

test("concurrent window saves cannot lose the hub or fail a queued update", async () => {
  const store = new JSONStore(join(await mkdtemp(join(tmpdir(), "persona-desktop-state-")), "desktop-state.json"));
  const file = Reflect.get(store, "file") as string;
  await Promise.all([
    store.saveWindow({ x: 10, y: 20, width: 1000, height: 700, maximized: false }),
    store.update({ hub: "http://127.0.0.1:12345/" }),
    store.saveWindow({ x: 30, y: 40, width: 1000, height: 700, maximized: false }),
  ]);
  const state = JSON.parse(await readFile(file, "utf8"));
  assert.equal(state.hub, "http://127.0.0.1:12345/");
  assert.equal(state.window.x, 30);
});

test("notification routes cannot leave the hub", () => {
  assert.equal(notificationRoute("/s/computer/session?work=1"), "/s/computer/session?work=1");
  assert.equal(notificationRoute("https://evil.test"), undefined);
  assert.equal(notificationRoute("//evil.test"), undefined);
});
