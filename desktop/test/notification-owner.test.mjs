import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
import test from "node:test";

test("retiring a hub window closes its own notifications even after IPC ownership moved", async () => {
  const root = join(import.meta.dirname, "..", "..");
  const script = `
    const { DesktopNotifications, installDesktopIPC } = await import("../dist-test/src/ipc.js");
    const opened = [];
    const notifications = new DesktopNotifications(route => opened.push(route));
    const hub = { url: new URL("https://old.example/"), prefix: "" };
    if (!notifications.show({ title: "old", tag: "old", route: "/s/computer/session" }, hub)) throw new Error("synthetic notification was refused");
    notifications.closeAll();
    const window = { webContents: {}, isDestroyed: () => false };
    const first = installDesktopIPC(window, { hub: () => hub, notifications: { closeAll: () => opened.push("closed-first") }, presence: () => ({ active: false, notifies: false }), openLocalSettings() {} });
    const second = installDesktopIPC(window, { hub: () => hub, notifications: { closeAll() {} }, presence: () => ({ active: false, notifies: false }), openLocalSettings() {} });
    first();
    second();
    if (JSON.stringify(opened) !== JSON.stringify(["closed-first"])) throw new Error(JSON.stringify(opened));
    process.exit(0);
  `;
  const child = spawn(process.execPath, ["--import", pathToFileURL(join(root, "desktop", "test", "fixtures", "lifecycle-register.mjs")).href, "--input-type=module", "--eval", script], { cwd: join(root, "desktop", "test"), stdio: ["ignore", "pipe", "pipe"] });
  let stderr = "";
  child.stderr.on("data", chunk => { stderr += chunk; });
  assert.equal(await new Promise(done => child.once("close", done)), 0, stderr);
});
